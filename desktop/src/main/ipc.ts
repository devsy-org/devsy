import { execFile } from "node:child_process"
import { existsSync } from "node:fs"
import { mkdir, readdir, readFile } from "node:fs/promises"
import { homedir } from "node:os"
import { join } from "node:path"
import { promisify } from "node:util"
import type { BrowserWindow } from "electron"
import { app, dialog, ipcMain } from "electron"
import type { CLIError, OperationStatus } from "../shared/cli-error.js"
import {
  normalizeOperationStatus,
  parseCliEnvelope,
} from "../shared/cli-error.js"
import { hashWorkspaceRef, trackEvent } from "./analytics.js"
import type { CliRunner } from "./cli.js"
import { loadCatalog } from "./image-catalog.js"
import type { LogStore } from "./log-store.js"
import type { MachineDiagnosticsStore } from "./machine-diagnostics-store.js"
import type { MachineDiagnosticsManager } from "./machine-diagnostics-manager.js"
import type { ProviderActivity, ProviderJobs } from "./provider-jobs.js"
import type { PtyManager } from "./pty.js"
import { sanitizeAppSettingsPatch } from "./app-settings.js"
import type { SettingsService } from "./settings-service.js"
import type { DaemonState } from "./state.js"
import {
  checkForUpdates,
  switchReleaseChannel,
  downloadUpdate,
  getAutoDownloadEnabled,
  getLastStatus,
  getReleaseChannel,
  installUpdate,
  type ReleaseChannel,
  setAutoDownloadEnabled,
} from "./updater.js"
import { type ProviderEntry, parseProviderEntries } from "./watcher.js"
import { normalizeWorkspaceStatus } from "./workspace-status.js"
import type { WorkspaceActivity } from "../shared/workspace-operation.js"
import type { WorkspaceJobs } from "./workspace-jobs.js"

const execFileAsync = promisify(execFile)

interface SecretEntry {
  name: string
  context: string
  created?: string
  lastUsed?: string
  orphaned?: boolean
  backend?: "keyring" | "file"
  attached?: boolean
}

interface EnvEntry {
  name: string
  value: string
  context: string
  attached: boolean
}

function dockerArch(nodeArch: string): string {
  if (nodeArch === "x64") return "amd64"
  if (nodeArch === "arm") return "arm"
  return nodeArch
}

// Cache for provider update checks. Seeded on launch and refreshed every 6 hours.
type UpdateInfo = {
  current: string
  latest: string
  updateAvailable: boolean
  unsupported: boolean
  error?: string
}

let providerUpdateCache: Record<string, UpdateInfo> = {}
let providerUpdateCacheCheckedAt: string | null = null

const IMAGE_CATALOG_URL =
  process.env.DEVSY_IMAGE_CATALOG_URL ??
  "https://raw.githubusercontent.com/devsy-org/devsy/main/desktop/resources/image-catalog-seed.json"
const IMAGE_CATALOG_TTL_MS = 24 * 60 * 60 * 1000

function imageCatalogPaths(): { cachePath: string; seedPath: string } {
  return {
    cachePath: join(app.getPath("userData"), "image-catalog.json"),
    seedPath: app.isPackaged
      ? join(process.resourcesPath, "image-catalog-seed.json")
      : join(app.getAppPath(), "resources", "image-catalog-seed.json"),
  }
}

interface SshKeyInfo {
  name: string
  keyType: string
  fingerprint: string
  comment: string
  publicKey: string
  path: string
  hasPassphrase: boolean
}

interface IpcDependencies {
  cli: CliRunner
  state: DaemonState
  logStore: LogStore
  machineDiagnosticsStore?: MachineDiagnosticsStore
  machineDiagnosticsManager?: MachineDiagnosticsManager
  pty: PtyManager
  getMainWindow: () => BrowserWindow | null
  providerJobs: ProviderJobs
  workspaceJobs: WorkspaceJobs
  workspaceSnapshot?: () => unknown
  onRendererReady?: (sender: Electron.WebContents) => void
  settingsService?: SettingsService
}

/** Format a line in zap console format so log-parser.ts can parse it. */
function formatLogLine(line: string, level: "INFO" | "ERROR" = "INFO"): string {
  return `${new Date().toISOString()}\t${level}\t${line}`
}

interface ProgressSink {
  line(formatted: string): boolean
  done(
    finalLine: string,
    extra?: {
      level?: "info" | "warn" | "error"
      cliError?: CLIError
      success?: boolean
    },
  ): Promise<void>
}

function redactSensitiveText(value: string): string {
  const secrets = Object.entries(process.env)
    .filter(
      ([name, secret]) =>
        secret &&
        /(PASSWORD|PASSWD|TOKEN|SECRET|API_KEY|APIKEY|AUTH|CREDENTIAL|PRIVATE_KEY|ACCESS_KEY)/i.test(
          name,
        ),
    )
    .map(([, secret]) => secret as string)
    .sort((a, b) => b.length - a.length)
  const redacted = secrets.reduce(
    (text, secret) => text.split(secret).join("***"),
    value,
  )
  return redacted
    .replace(/(https?:\/\/)[^\s/@]+@/gi, "$1***@")
    .replace(/(authorization\s*[:=]\s*(?:bearer|basic)\s+)[^\s,]+/gi, "$1***")
}

function redactCLIError(error: CLIError): CLIError {
  return {
    ...error,
    message: redactSensitiveText(error.message),
    hint: error.hint ? redactSensitiveText(error.hint) : undefined,
    context: error.context
      ? Object.fromEntries(
          Object.entries(error.context).map(([key, value]) => [
            redactSensitiveText(key),
            redactSensitiveText(value),
          ]),
        )
      : undefined,
  }
}

function redactOperationStatus(value: OperationStatus): OperationStatus {
  return {
    ...value,
    phase: redactSensitiveText(value.phase),
    step: value.step ? redactSensitiveText(value.step) : undefined,
    operationId: value.operationId
      ? redactSensitiveText(value.operationId)
      : undefined,
    parentOperationId: value.parentOperationId
      ? redactSensitiveText(value.parentOperationId)
      : undefined,
    error: value.error
      ? {
          ...value.error,
          code: value.error.code
            ? redactSensitiveText(value.error.code)
            : undefined,
          message: redactSensitiveText(value.error.message),
          hint: value.error.hint
            ? redactSensitiveText(value.error.hint)
            : undefined,
          context: value.error.context
            ? Object.fromEntries(
                Object.entries(value.error.context).map(([key, text]) => [
                  redactSensitiveText(key),
                  redactSensitiveText(text),
                ]),
              )
            : undefined,
        }
      : undefined,
  }
}

function createLogSink(
  getWin: () => BrowserWindow | null,
  commandId: string,
  appendLog?: (line: string) => boolean,
  flush?: () => Promise<void>,
): ProgressSink {
  const FLUSH_MS = 64
  const MAX_BATCH = 250
  let buf: string[] = []
  let finished = false
  let timer: ReturnType<typeof setTimeout> | null = null

  function post(
    done: boolean,
    extra?: {
      message?: string
      level?: "info" | "warn" | "error"
      cliError?: CLIError
      success?: boolean
    },
  ): void {
    if (timer) {
      clearTimeout(timer)
      timer = null
    }
    if (!done && buf.length === 0) return
    const lines = buf
    buf = []
    const win = getWin()
    if (!win || win.isDestroyed?.()) return
    win.webContents.send("command-progress", {
      commandId,
      lines: lines.map(redactSensitiveText),
      done,
      ...(extra
        ? {
            ...extra,
            message: extra.message
              ? redactSensitiveText(extra.message)
              : undefined,
            cliError: extra.cliError
              ? redactCLIError(extra.cliError)
              : undefined,
          }
        : {}),
    })
  }

  return {
    line(formatted) {
      if (finished) return true
      const safeLine = redactSensitiveText(formatted)
      const ok = appendLog?.(safeLine) ?? true
      buf.push(safeLine)
      if (buf.length >= MAX_BATCH) post(false)
      else if (!timer) timer = setTimeout(() => post(false), FLUSH_MS)
      return ok
    },
    async done(finalLine, extra) {
      if (finished) return
      finished = true
      const safeLine = redactSensitiveText(finalLine)
      buf.push(safeLine)
      try {
        appendLog?.(safeLine)
        await flush?.()
      } catch (error) {
        // Log persistence must not change a command's actual outcome.
        console.warn("[workspace-operation] log flush failed", error)
      }
      post(true, { message: finalLine, ...extra })
    },
  }
}

export function registerIpcHandlers(deps: IpcDependencies): {
  tunnelProcesses: Map<string, import("node:child_process").ChildProcess>
  scheduleProviderUpdateCheck: () => void
  runInitialProviderUpdateCheck: () => void
  workspaceActions: {
    stop: (workspaceId: string) => Promise<void>
    start: (workspaceId: string) => Promise<void>
  }
} {
  const {
    cli,
    state,
    logStore,
    pty,
    providerJobs,
    workspaceJobs,
    machineDiagnosticsStore,
    machineDiagnosticsManager,
    getMainWindow,
  } = deps
  const tunnelProcesses = new Map<
    string,
    import("node:child_process").ChildProcess
  >()
  // Maps workspaceId -> task id for still-running `up --detach` submissions.
  const activeUpTasks = new Map<string, string>()
  // Serializes the cancel/submit/register sequence per workspace.
  const upSubmitChains = new Map<string, Promise<unknown>>()

  /**
   * Run fn after any prior invocation for the same workspace has settled.
   * Concurrent `up` submissions would otherwise both pass the cancel step
   * before either registers its task, leaving the first one running with no
   * map entry to cancel it by.
   */
  function serializePerWorkspace<T>(
    workspaceId: string,
    fn: () => Promise<T>,
  ): Promise<T> {
    // Chain entries never reject, so a failed run doesn't poison the queue.
    const prev = upSubmitChains.get(workspaceId) ?? Promise.resolve()
    const next = prev.then(fn)
    const settled = next.then(
      () => undefined,
      () => undefined,
    )
    upSubmitChains.set(workspaceId, settled)
    void settled.then(() => {
      if (upSubmitChains.get(workspaceId) === settled) {
        upSubmitChains.delete(workspaceId)
      }
    })
    return next
  }

  /**
   * Cancel a workspace's in-flight `up` task and terminate its status-follow
   * child, awaiting exit so the handle is never dropped while the process is
   * still alive (which would orphan it beyond the reach of any later kill).
   */
  async function cancelActiveUp(workspaceId: string): Promise<void> {
    const taskId = activeUpTasks.get(workspaceId)
    if (taskId) {
      // Retain the mapping until the cancel lands, and let a failure reject.
      await cli.run(["workspace", "task", "cancel", taskId])
      if (activeUpTasks.get(workspaceId) === taskId) {
        activeUpTasks.delete(workspaceId)
      }
    }

    const tunnelProc = tunnelProcesses.get(workspaceId)
    if (tunnelProc) {
      tunnelProcesses.delete(workspaceId)
      let settled = false
      const tunnelExit = new Promise<void>((resolve) => {
        let timer: ReturnType<typeof setTimeout> | null = setTimeout(() => {
          timer = null
          settled = true
          resolve()
        }, 2000)

        if (tunnelProc.exitCode !== null || tunnelProc.signalCode !== null) {
          if (timer) clearTimeout(timer)
          settled = true
          resolve()
          return
        }
        tunnelProc.once("close", () => {
          if (timer) {
            clearTimeout(timer)
            timer = null
          }
          settled = true
          resolve()
        })
      })
      tunnelProc.kill("SIGTERM")
      await tunnelExit
      // If process did not close in time, forcefully kill and suppress any late callbacks
      if (
        !settled ||
        (tunnelProc.exitCode === null && tunnelProc.signalCode === null)
      ) {
        // Suppress workspace callbacks from the onLine handler
        const suppressWorkspaceFn = (
          tunnelProc as unknown as { _suppressWorkspaceCallbacks?: () => void }
        )._suppressWorkspaceCallbacks
        if (suppressWorkspaceFn) suppressWorkspaceFn()

        // Suppress callbacks at the readline level
        const suppressFn = (
          tunnelProc as unknown as { _suppressCallbacks?: () => void }
        )._suppressCallbacks
        if (suppressFn) suppressFn()

        // Close readline interfaces
        const rlStdout = (
          tunnelProc as unknown as { _rlStdout?: { close: () => void } }
        )._rlStdout
        const rlStderr = (
          tunnelProc as unknown as { _rlStderr?: { close: () => void } }
        )._rlStderr
        if (rlStdout) rlStdout.close()
        if (rlStderr) rlStderr.close()

        // Destroy streams to stop emitting data events
        if (tunnelProc.stdout) {
          tunnelProc.stdout.removeAllListeners()
          tunnelProc.stdout.destroy()
        }
        if (tunnelProc.stderr) {
          tunnelProc.stderr.removeAllListeners()
          tunnelProc.stderr.destroy()
        }

        tunnelProc.removeAllListeners()
        tunnelProc.kill("SIGKILL")
      }
    }
  }

  /**
   * Terminate every desktop-spawned process tied to a workspace and wait for
   * them to actually exit. Called before stop/delete so destructive CLI runs
   * don't race with in-flight children that are still appending to the
   * workspace's log directory.
   */
  async function quiesceWorkspace(workspaceId: string): Promise<void> {
    await serializePerWorkspace(workspaceId, async () => {
      await cancelActiveUp(workspaceId)
      await Promise.all([
        cli.cancelFor(workspaceId),
        pty.cancelFor(workspaceId),
      ])
    })
  }

  function errorMessage(error: unknown): string {
    return error instanceof Error ? error.message : String(error)
  }

  function cliErrorOrFallback(error: unknown, code: string): CLIError {
    const cliError = (error as { cliError?: CLIError }).cliError
    return cliError ?? { code, message: errorMessage(error) }
  }

  function forwardWorkspaceStatus(
    commandId: string,
    workspaceId: string,
    line: string,
    stream: "stdout" | "stderr",
  ): boolean {
    if (stream !== "stdout") return false
    const envelope = parseCliEnvelope(line)
    if (envelope?.kind !== "status") return false
    if (!workspaceJobs.owns(workspaceId, commandId)) return true
    const status = redactOperationStatus(normalizeOperationStatus(envelope))
    workspaceJobs.progress(workspaceId, commandId, status)
    deps
      .getMainWindow()
      ?.webContents.send("workspace-status", {
        commandId,
        workspaceId,
        ...status,
      })
    return true
  }

  /**
   * Track a provider job for the duration of fn, so the job cannot outlive the
   * work it describes. Opening a job in one place and closing it in another
   * leaves the card spinning forever on any path that forgets.
   *
   * Errors are recorded on the job and rethrown, leaving the caller's own
   * error handling intact.
   */
  async function withProviderJob<T>(
    name: string,
    activity: ProviderActivity,
    fn: () => Promise<T>,
  ): Promise<T> {
    providerJobs.start(name, activity)
    let result: T
    try {
      result = await fn()
    } catch (error) {
      const cliError = (error as { cliError?: CLIError }).cliError
        ? redactCLIError((error as { cliError: CLIError }).cliError)
        : undefined
      await providerJobs.finish(
        name,
        cliError ??
          redactCLIError({
            code: "provider_failed",
            message: errorMessage(error),
          }),
      )
      throw error
    }
    await providerJobs.finish(name)
    return result
  }

  /**
   * Run a provider CLI command, feeding its NDJSON status lines into the job
   * registry so the UI tracks phases as they happen instead of only learning
   * the outcome at exit.
   */
  function runProviderWithStatus(
    name: string,
    cliArgs: string[],
  ): Promise<void> {
    return new Promise((resolve, reject) => {
      cli
        .runStreaming(
          cliArgs,
          (line, stream) => {
            if (stream !== "stdout") return
            const envelope = parseCliEnvelope(line)
            if (envelope?.kind === "status") {
              const status = redactOperationStatus(
                normalizeOperationStatus(envelope),
              )
              providerJobs.reportStatus(name, status)
            }
          },
          (code, cliError) => {
            if (code === 0) {
              resolve()
              return
            }
            const message =
              cliError?.message ?? `${cliArgs.join(" ")} exited with ${code}`
            reject(Object.assign(new Error(message), { cliError }))
          },
        )
        .catch(reject)
    })
  }

  /**
   * Compute provider update information by querying the CLI for all installed providers.
   */
  async function computeUpdateChecks(): Promise<Record<string, UpdateInfo>> {
    const providers = state.providerList()
    const out: Record<string, UpdateInfo> = {}
    await Promise.all(
      providers.map(async (p) => {
        const version = typeof p.version === "string" ? p.version : ""
        try {
          const versions = await cli.run<
            Array<{ tag: string; current?: boolean }>
          >(["provider", "versions", p.name, "--json", "--no-cache"])
          const list = versions ?? []
          const current = list.find((v) => v.current)?.tag ?? version
          const latest = list[0]?.tag ?? ""
          out[p.name] = {
            current,
            latest,
            updateAvailable: latest !== "" && latest !== current,
            unsupported: false,
          }
        } catch (err) {
          const msg = err instanceof Error ? err.message : String(err)
          if (msg.includes("does not support version listing")) {
            out[p.name] = {
              current: version,
              latest: "",
              updateAvailable: false,
              unsupported: true,
            }
          } else {
            out[p.name] = {
              current: version,
              latest: "",
              updateAvailable: false,
              unsupported: false,
              error: msg,
            }
          }
        }
      }),
    )
    return out
  }

  // ── Workspaces ──
  ipcMain.handle("workspace_list", () => state.workspaceList())
  ipcMain.handle(
    "workspace_snapshot",
    () =>
      deps.workspaceSnapshot?.() ?? {
        workspaces: state.workspaceList(),
        jobs: workspaceJobs.snapshot(),
        revision: workspaceJobs.revision,
      },
  )
  ipcMain.handle("workspace_refresh", (_event, args: { workspaceId: string }) =>
    workspaceJobs.retryRefresh(args.workspaceId),
  )

  ipcMain.handle(
    "workspace_status",
    async (_event, args: { workspaceId: string; recovery?: boolean }) => {
      const cliArgs = [
        "workspace",
        "status",
        args.workspaceId,
        "--result-format",
        "json",
        "--timeout",
        "15s",
      ]
      if (args.recovery) cliArgs.push("--recovery")
      const generation = workspaceJobs.generation(args.workspaceId)
      const raw = await cli.runRaw(cliArgs)
      if (
        !args.recovery &&
        generation === workspaceJobs.generation(args.workspaceId)
      ) {
        const status = normalizeWorkspaceStatus(raw)
        if (status && state.updateWorkspaceStatus(args.workspaceId, status)) {
          // The watcher owns renderer broadcasts; this update still keeps
          // main-process consumers current for explicit status requests.
        }
      }
      return raw
    },
  )

  ipcMain.handle(
    "workspace_rename",
    async (_event, args: { workspaceId: string; newWorkspaceId: string }) => {
      trackEvent("workspace_rename", {
        workspace_ref: hashWorkspaceRef(args.workspaceId),
      })
      await cli.runRaw([
        "workspace",
        "rename",
        args.workspaceId,
        args.newWorkspaceId,
      ])
    },
  )

  ipcMain.handle(
    "workspace_set_ide",
    async (_event, args: { workspaceId: string; ide: string }) => {
      trackEvent("workspace_set_ide", {
        ide: args.ide,
        workspace_ref: hashWorkspaceRef(args.workspaceId),
      })
      await cli.runRaw(["workspace", "set-ide", args.workspaceId, args.ide])
    },
  )

  // ── Providers ──
  ipcMain.handle("provider_list", async () => {
    const raw = await cli.run<Record<string, ProviderEntry>>([
      "provider",
      "list",
    ])
    const providers = parseProviderEntries(raw)
    state.updateProviders(providers as any[])
    return state.providerList()
  })

  ipcMain.handle(
    "provider_add",
    async (
      _event,
      args: { name: string; source?: string; singleMachine?: boolean },
    ) => {
      trackEvent("provider_add")
      const src = args.source ?? args.name
      const cliArgs = ["provider", "add", src, "--use=false"]
      if (args.source) {
        cliArgs.push("--name", args.name)
      }
      if (args.singleMachine) {
        cliArgs.push("--single-machine")
      }

      // Not withProviderJob: the job outlives this call so the badge stays busy
      // until the wizard's chained provider_init finishes. The opener closes it,
      // via provider_release_job on any path that abandons the install.
      providerJobs.start(args.name, "installing")
      try {
        await runProviderWithStatus(args.name, cliArgs)
      } catch (error) {
        const cliError = (error as { cliError?: CLIError }).cliError
        await providerJobs.finish(
          args.name,
          redactCLIError(
            cliError ?? {
              code: "provider_failed",
              message: errorMessage(error),
            },
          ),
        )
        throw error
      }
    },
  )

  ipcMain.handle("provider_delete", async (_event, args: { name: string }) => {
    trackEvent("provider_delete")
    await cli.runRaw(["provider", "delete", args.name])
    // Drop any retained failure so a re-added provider starts clean.
    providerJobs.clear(args.name)
  })

  // Releases a job the caller opened but will not finish — e.g. the wizard
  // installs a provider, then the user skips initialization or closes the
  // dialog. Without this the card would spin on "installing…" indefinitely.
  ipcMain.handle(
    "provider_release_job",
    async (_event, args: { name: string }) => {
      await providerJobs.finish(args.name)
    },
  )

  ipcMain.handle("provider_use", async (_event, args: { name: string }) => {
    await cli.runRaw(["provider", "use", args.name])
  })

  // Returns an envelope rather than throwing so a structured cliError survives
  // the IPC boundary. Electron's structured-clone only preserves
  // name/message/stack/cause on Error instances and drops arbitrary
  // own-properties, so a thrown Error with a .cliError attached would lose it.
  ipcMain.handle("provider_init", async (_event, args: { name: string }) => {
    try {
      await withProviderJob(args.name, "initializing", () =>
        runProviderWithStatus(args.name, ["provider", "init", args.name]),
      )
      return { ok: true } as const
    } catch (err) {
      const cliError = (err as { cliError?: CLIError }).cliError
      return {
        ok: false,
        message: redactSensitiveText(errorMessage(err)),
        cliError: cliError ? redactCLIError(cliError) : undefined,
      } as const
    }
  })

  ipcMain.handle(
    "provider_init_streaming",
    async (_event, args: { name: string }) => {
      const cmdId = crypto.randomUUID()
      const win = deps.getMainWindow()

      // Not withProviderJob: the job outlives the handler, closed from onExit.
      providerJobs.start(args.name, "initializing")
      await cli.runStreaming(
        ["provider", "init", args.name],
        (line, stream, meta) => {
          // Status envelopes drive the job registry; everything else is log
          // text for the wizard's output pane.
          if (stream === "stdout") {
            const envelope = parseCliEnvelope(line)
            if (envelope?.kind === "status") {
              const status = redactOperationStatus(
                normalizeOperationStatus(envelope),
              )
              providerJobs.reportStatus(args.name, status)
              return
            }
          }
          const formatted = redactSensitiveText(formatLogLine(line))
          win?.webContents.send("command-progress", {
            commandId: cmdId,
            message: formatted,
            level: meta?.level,
            done: false,
          })
        },
        async (code, cliError) => {
          // Finish first: it refreshes the provider list, so the wizard's
          // done signal can't arrive while the card still reads uninitialized.
          await providerJobs.finish(
            args.name,
            code === 0
              ? undefined
              : redactCLIError(
                  cliError ?? {
                    code: "provider_init_failed",
                    message: `provider init exited with ${code}`,
                  },
                ),
          )
          const exitMsg = redactSensitiveText(
            formatLogLine(`Exit code: ${code}`, code === 0 ? "INFO" : "ERROR"),
          )
          win?.webContents.send("command-progress", {
            commandId: cmdId,
            message: exitMsg,
            level: code === 0 ? "info" : "error",
            success: code === 0,
            cliError: cliError ? redactCLIError(cliError) : undefined,
            done: true,
          })
        },
      )

      return cmdId
    },
  )

  // set-source installs replacement binaries and clears Initialized, since
  // the new binary has not run its init. Chain init so the provider ends up
  // usable rather than sitting in a needs-re-init state the user must notice.
  ipcMain.handle("provider_update", async (_event, args: { name: string }) => {
    await withProviderJob(args.name, "updating", async () => {
      await runProviderWithStatus(args.name, [
        "provider",
        "set-source",
        args.name,
        "--use=false",
      ])
      await runProviderWithStatus(args.name, ["provider", "init", args.name])
    })
  })

  ipcMain.handle(
    "provider_update_streaming",
    async (_event, args: { name: string }) => {
      const cmdId = crypto.randomUUID()
      const win = deps.getMainWindow()
      providerJobs.start(args.name, "updating")

      const sendProgress = (message: string, level?: string) => {
        const formatted = redactSensitiveText(formatLogLine(message))
        providerJobs.appendLog(args.name, formatted)
        win?.webContents.send("command-progress", {
          commandId: cmdId,
          message: formatted,
          level,
          done: false,
        })
      }

      const runStep = (cliArgs: string[]): Promise<void> =>
        new Promise((resolve, reject) => {
          cli
            .runStreaming(
              cliArgs,
              (line, stream, meta) => {
                if (stream === "stdout") {
                  const envelope = parseCliEnvelope(line)
                  if (envelope?.kind === "status") {
                    providerJobs.reportStatus(
                      args.name,
                      redactOperationStatus(normalizeOperationStatus(envelope)),
                    )
                    return
                  }
                }
                sendProgress(line, meta?.level)
              },
              (code, cliError) => {
                if (code === 0) {
                  resolve()
                  return
                }
                reject(
                  Object.assign(
                    new Error(
                      cliError?.message ??
                        `${cliArgs.join(" ")} exited with ${code}`,
                    ),
                    { cliError },
                  ),
                )
              },
            )
            .catch(reject)
        })

      void (async () => {
        let failure: CLIError | undefined
        try {
          sendProgress("Downloading provider update", "info")
          await runStep(["provider", "set-source", args.name, "--use=false"])
          sendProgress("Initializing updated provider", "info")
          await runStep(["provider", "init", args.name])
        } catch (error) {
          failure = (error as { cliError?: CLIError }).cliError ?? {
            code: "provider_update_failed",
            message: errorMessage(error),
          }
        }
        try {
          await providerJobs.finish(
            args.name,
            failure ? redactCLIError(failure) : undefined,
          )
        } catch (error) {
          failure = {
            code: "provider_refresh_failed",
            message:
              "The provider updated, but its current state could not be refreshed.",
            hint: "Refresh provider status to try again.",
            context: { cause: errorMessage(error) },
          }
          await providerJobs.finish(args.name, redactCLIError(failure))
        }
        win?.webContents.send("command-progress", {
          commandId: cmdId,
          message: redactSensitiveText(
            formatLogLine(
              failure ? "Provider update failed" : "Provider update complete",
              failure ? "ERROR" : "INFO",
            ),
          ),
          level: failure ? "error" : "info",
          success: !failure,
          cliError: failure ? redactCLIError(failure) : undefined,
          done: true,
        })
      })()

      return cmdId
    },
  )

  ipcMain.handle(
    "provider_refresh_state",
    async (_event, args: { name: string }) => {
      try {
        await providerJobs.retryRefresh(args.name)
        return { ok: true } as const
      } catch (error) {
        return { ok: false, message: errorMessage(error) } as const
      }
    },
  )

  ipcMain.handle("provider_options", async (_event, args: { name: string }) => {
    return cli.run(["provider", "get", args.name])
  })

  ipcMain.handle(
    "provider_set_options",
    async (_event, args: { name: string; options: string[] }) => {
      const cliArgs = ["provider", "set", args.name, "--skip-init"]
      for (const opt of args.options) {
        cliArgs.push("-o", opt)
      }
      await cli.runRaw(cliArgs)
    },
  )

  ipcMain.handle(
    "provider_set_single_machine",
    async (_event, args: { name: string; enabled: boolean }) => {
      await cli.runRaw([
        "provider",
        "set",
        args.name,
        "--skip-init",
        `--single-machine=${args.enabled}`,
      ])
    },
  )

  ipcMain.handle(
    "provider_rename",
    async (_event, args: { name: string; newName: string }) => {
      await cli.runRaw(["provider", "rename", args.name, args.newName])
    },
  )

  ipcMain.handle(
    "provider_list_versions",
    async (_event, args: { name: string; noCache?: boolean }) => {
      const cliArgs = ["provider", "versions", args.name, "--json"]
      if (args.noCache) cliArgs.push("--no-cache")
      try {
        const versions = await cli.run<unknown[]>(cliArgs)
        return { versions: versions ?? [], unsupported: false }
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err)
        if (msg.includes("does not support version listing")) {
          return { versions: [], unsupported: true }
        }
        return { versions: [], unsupported: false, error: msg }
      }
    },
  )

  ipcMain.handle(
    "provider_set_version",
    async (_event, args: { name: string; tag: string }) => {
      // Pinning a version swaps binaries via the same update path, so it
      // clears Initialized and needs the same re-init as a source change.
      await withProviderJob(args.name, "updating", async () => {
        await runProviderWithStatus(args.name, [
          "provider",
          "set-source",
          args.name,
          "--version",
          args.tag,
        ])
        await runProviderWithStatus(args.name, ["provider", "init", args.name])
      })
    },
  )

  ipcMain.handle("provider_check_updates", async () => {
    const out = await computeUpdateChecks()
    providerUpdateCache = out
    providerUpdateCacheCheckedAt = new Date().toISOString()
    return out
  })

  ipcMain.handle("provider_get_update_cache", async () => ({
    updates: providerUpdateCache,
    lastCheckedAt: providerUpdateCacheCheckedAt,
  }))

  ipcMain.handle("image_catalog_get", async () => {
    const { cachePath, seedPath } = imageCatalogPaths()
    return loadCatalog({
      url: IMAGE_CATALOG_URL,
      cachePath,
      seedPath,
      ttlMs: IMAGE_CATALOG_TTL_MS,
      force: false,
    })
  })

  ipcMain.handle("image_catalog_refresh", async () => {
    const { cachePath, seedPath } = imageCatalogPaths()
    return loadCatalog({
      url: IMAGE_CATALOG_URL,
      cachePath,
      seedPath,
      ttlMs: IMAGE_CATALOG_TTL_MS,
      force: true,
    })
  })

  ipcMain.handle("dialog_open_directory", async () => {
    const win = deps.getMainWindow()
    const result = win
      ? await dialog.showOpenDialog(win, { properties: ["openDirectory"] })
      : await dialog.showOpenDialog({ properties: ["openDirectory"] })
    if (result.canceled || result.filePaths.length === 0) return null
    return result.filePaths[0]
  })

  // ── Machines ──
  ipcMain.handle("machine_list", () => state.machineList())

  ipcMain.handle(
    "machine_create",
    async (
      _event,
      args: {
        name: string
        provider: string
        options?: Record<string, string>
      },
    ) => {
      const cliArgs = [
        "machine",
        "create",
        args.name,
        "--provider",
        args.provider,
      ]
      for (const [k, v] of Object.entries(args.options ?? {})) {
        cliArgs.push("--option", `${k}=${v}`)
      }
      await cli.runRaw(cliArgs)
    },
  )

  ipcMain.handle(
    "machine_delete",
    async (_event, args: { id: string; force?: boolean }) => {
      const key = { context: state.currentContext(), machineId: args.id }
      const cliArgs = ["machine", "delete", args.id, "--context", key.context]
      if (args.force) cliArgs.push("--force")
      await cli.runRaw(cliArgs)
      machineDiagnosticsManager?.delete(key)
    },
  )

  ipcMain.handle("machine_start", async (_event, args: { id: string }) => {
    await cli.runRaw(["machine", "start", args.id])
  })

  ipcMain.handle("machine_stop", async (_event, args: { id: string }) => {
    const key = { context: state.currentContext(), machineId: args.id }
    await cli.runRaw(["machine", "stop", args.id, "--context", key.context])
    machineDiagnosticsManager?.markStopped(key)
  })

  ipcMain.handle("machine_status", async (_event, args: { id: string }) => {
    return cli.runRaw(["machine", "status", args.id, "--result-format", "json"])
  })

  ipcMain.handle("machine_diagnostics_get", (_event, args: { id: string }) => {
    return (
      machineDiagnosticsManager?.getCached({
        context: state.currentContext(),
        machineId: args.id,
      }) ?? null
    )
  })

  ipcMain.handle(
    "machine_diagnostics_refresh",
    async (_event, args: { id: string }) => {
      if (!machineDiagnosticsManager)
        throw new Error("machine diagnostics manager is unavailable")
      const key = { context: state.currentContext(), machineId: args.id }
      const merged = await machineDiagnosticsManager.refresh(key)
      getMainWindow()?.webContents.send("machine-diagnostics-changed", {
        machineId: args.id,
        context: key.context,
        diagnostics: merged,
      })
      return merged
    },
  )

  // ── Contexts ──
  ipcMain.handle("context_list", () => state.contextList())

  ipcMain.handle("context_use", async (_event, args: { name: string }) => {
    await cli.runRaw(["context", "use", args.name])
  })

  ipcMain.handle(
    "context_options",
    async (_event, args: { context?: string }) => {
      const cliArgs = ["context", "get"]
      if (args.context) cliArgs.push("--context", args.context)
      return cli.run(cliArgs)
    },
  )

  ipcMain.handle(
    "context_set_options",
    async (_event, args: { options: string[]; context?: string }) => {
      const cliArgs: string[] = ["context", "set"]
      if (args.context) cliArgs.push(args.context)
      for (const opt of args.options) {
        cliArgs.push("-o", opt)
      }
      await cli.runRaw(cliArgs)
    },
  )

  ipcMain.handle("context_create", async (_event, args: { name: string }) => {
    // The Go CLI's `context create` auto-activates the new context.
    // Restore the previous active context so creating doesn't switch away.
    const { activeContext: prev } = state.contextList()
    await cli.runRaw(["context", "create", args.name])
    if (prev) {
      await cli.runRaw(["context", "use", prev])
    }
  })

  ipcMain.handle("context_delete", async (_event, args: { name: string }) => {
    await cli.runRaw(["context", "delete", args.name])
  })

  ipcMain.handle("secret_list", async () =>
    cli.run<SecretEntry[]>(["secret", "list"]),
  )

  // Returns an envelope rather than throwing so a structured cliError survives
  // the IPC boundary (see provider_init above).
  ipcMain.handle(
    "secret_set",
    async (_event, args: { name: string; value: string }) => {
      trackEvent("secret_set")
      try {
        await cli.runRawStdin(
          ["secret", "set", args.name, "--stdin"],
          args.value,
        )
        return { ok: true } as const
      } catch (err) {
        const cliError = (err as { cliError?: CLIError }).cliError
        const message = err instanceof Error ? err.message : String(err)
        return { ok: false, message, cliError } as const
      }
    },
  )

  ipcMain.handle("secret_delete", async (_event, args: { name: string }) => {
    trackEvent("secret_delete")
    try {
      await cli.runRaw(["secret", "delete", args.name])
      return { ok: true } as const
    } catch (err) {
      const cliError = (err as { cliError?: CLIError }).cliError
      const message = err instanceof Error ? err.message : String(err)
      return { ok: false, message, cliError } as const
    }
  })

  ipcMain.handle(
    "secret_attach",
    async (_event, args: { name: string; context: string }) => {
      trackEvent("secret_attach")
      try {
        if (!args.context) throw new Error("context is required")
        await cli.runRaw([
          "--context",
          args.context,
          "secret",
          "attach",
          args.name,
        ])
        return { ok: true } as const
      } catch (err) {
        const cliError = (err as { cliError?: CLIError }).cliError
        const message = err instanceof Error ? err.message : String(err)
        return { ok: false, message, cliError } as const
      }
    },
  )

  ipcMain.handle(
    "secret_detach",
    async (_event, args: { name: string; context: string }) => {
      trackEvent("secret_detach")
      try {
        if (!args.context) throw new Error("context is required")
        await cli.runRaw([
          "--context",
          args.context,
          "secret",
          "detach",
          args.name,
        ])
        return { ok: true } as const
      } catch (err) {
        const cliError = (err as { cliError?: CLIError }).cliError
        const message = err instanceof Error ? err.message : String(err)
        return { ok: false, message, cliError } as const
      }
    },
  )

  ipcMain.handle("env_list", async () => cli.run<EnvEntry[]>(["env", "list"]))

  // Returns an envelope rather than throwing so a structured cliError survives
  // the IPC boundary (see provider_init above).
  ipcMain.handle(
    "env_set",
    async (_event, args: { name: string; value: string }) => {
      trackEvent("env_set")
      try {
        await cli.runRaw(["env", "set", args.name, "--value", args.value])
        return { ok: true } as const
      } catch (err) {
        const cliError = (err as { cliError?: CLIError }).cliError
        const message = err instanceof Error ? err.message : String(err)
        return { ok: false, message, cliError } as const
      }
    },
  )

  ipcMain.handle(
    "env_delete",
    async (_event, args: { name: string; context: string }) => {
      trackEvent("env_delete")
      try {
        if (!args.context) throw new Error("context is required")
        await cli.runRaw([
          "--context",
          args.context,
          "env",
          "delete",
          args.name,
        ])
        return { ok: true } as const
      } catch (err) {
        const cliError = (err as { cliError?: CLIError }).cliError
        const message = err instanceof Error ? err.message : String(err)
        return { ok: false, message, cliError } as const
      }
    },
  )

  ipcMain.handle(
    "env_attach",
    async (_event, args: { name: string; context: string }) => {
      trackEvent("env_attach")
      try {
        if (!args.context) throw new Error("context is required")
        await cli.runRaw([
          "--context",
          args.context,
          "env",
          "attach",
          args.name,
        ])
        return { ok: true } as const
      } catch (err) {
        const cliError = (err as { cliError?: CLIError }).cliError
        const message = err instanceof Error ? err.message : String(err)
        return { ok: false, message, cliError } as const
      }
    },
  )

  ipcMain.handle(
    "env_detach",
    async (_event, args: { name: string; context: string }) => {
      trackEvent("env_detach")
      try {
        if (!args.context) throw new Error("context is required")
        await cli.runRaw([
          "--context",
          args.context,
          "env",
          "detach",
          args.name,
        ])
        return { ok: true } as const
      } catch (err) {
        const cliError = (err as { cliError?: CLIError }).cliError
        const message = err instanceof Error ? err.message : String(err)
        return { ok: false, message, cliError } as const
      }
    },
  )

  // ── System ──
  ipcMain.handle("devsy_version", async () => {
    return cli.runRaw(["--version"])
  })

  ipcMain.handle("devsy_upgrade", async (_event, args: { version: string }) => {
    return cli.runRaw(["feature", "upgrade", "--version", args.version])
  })

  ipcMain.handle(
    "devsy_upgrade_dry_run",
    async (_event, args: { version: string }) => {
      return cli.runRaw([
        "feature",
        "upgrade",
        "--version",
        args.version,
        "--dry-run",
      ])
    },
  )

  // ── Logs ──
  ipcMain.handle(
    "workspace_logs_list",
    async (_event, args: { workspaceId: string }) => {
      return logStore.listLogs(
        state.workspaceContext(args.workspaceId),
        args.workspaceId,
      )
    },
  )

  ipcMain.handle(
    "workspace_log_read",
    async (_event, args: { workspaceId: string; filename: string }) => {
      return logStore.readLog(
        state.workspaceContext(args.workspaceId),
        args.workspaceId,
        args.filename,
      )
    },
  )

  ipcMain.handle(
    "workspace_log_delete",
    async (_event, args: { workspaceId: string; filename: string }) => {
      logStore.deleteLog(
        state.workspaceContext(args.workspaceId),
        args.workspaceId,
        args.filename,
      )
    },
  )

  const runWorkspaceUp = (args: {
    source: string
    workspaceId?: string
    provider?: string
    ide?: string
    ideLaunch?: "auto" | "headless" | "skip"
    debug?: boolean
    workspaceFolder?: string
    devcontainer?: string
    prebuildRepository?: string
    platform?: string
    recovery?: boolean
    commandId?: string
  }): { commandId: string; completion: Promise<void> } => {
    trackEvent("workspace_create", {
      provider: args.provider,
      workspace_ref: hashWorkspaceRef(args.workspaceId ?? args.source),
    })
    const cliArgs = ["workspace", "up", args.source]
    if (args.workspaceId) cliArgs.push("--id", args.workspaceId)
    if (args.provider) cliArgs.push("--provider", args.provider)
    if (args.ide) cliArgs.push("--ide", args.ide)
    if (args.ideLaunch) cliArgs.push("--ide-launch", args.ideLaunch)
    if (args.debug) cliArgs.push("--debug")
    if (args.workspaceFolder)
      cliArgs.push("--workspace-folder", args.workspaceFolder)
    if (args.devcontainer) cliArgs.push("--devcontainer", args.devcontainer)
    if (args.prebuildRepository)
      cliArgs.push("--prebuild-repo", args.prebuildRepository)
    if (args.platform) cliArgs.push("--platform", args.platform)
    if (args.recovery) cliArgs.push("--recovery")

    const wsId = args.workspaceId ?? args.source
    const cmdId = args.commandId ?? crypto.randomUUID()
    const activity = args.workspaceId ? "creating" : "starting"
    const generation = workspaceJobs.start(wsId, activity, cmdId)
    const started = performance.now()
    const timing = (stage: string) =>
      console.debug(
        `[workspace-operation] ${cmdId} ${stage} ${Math.round(performance.now() - started)}ms`,
      )
    timing("accepted")
    const completion = (async () => {
      const logPath = logStore.createLogFile(state.workspaceContext(wsId), wsId)
      const sink = createLogSink(
        deps.getMainWindow,
        cmdId,
        (line) => logStore.appendLog(logPath, line),
        () => logStore.closeLog(logPath),
      )

      await serializePerWorkspace(wsId, async () => {
        // Tear down any prior run for this workspace before starting a new
        // one.
        let taskId: string
        try {
          workspaceJobs.phase(wsId, cmdId, "Closing connections")
          await cancelActiveUp(wsId)
          timing("shutdown-complete")
          workspaceJobs.phase(wsId, cmdId, "Launching command")
          timing("cli-launch")
          // Submit: returns immediately with the background task's id.
          const submitted = await cli.run<{ kind: string; id: string }>([
            ...cliArgs,
            "--detach",
          ])
          if (!submitted?.id) {
            throw new Error("workspace up --detach returned no task id")
          }
          taskId = submitted.id
        } catch (error) {
          const err = error as Error & { cliError?: CLIError }
          void sink.done(formatLogLine(err.message, "ERROR"), {
            level: "error",
            success: false,
            cliError: err.cliError ?? {
              code: "up_failed",
              message: err.message,
            },
          })
          await workspaceJobs.finish(
            wsId,
            generation,
            redactCLIError(
              err.cliError ?? { code: "up_failed", message: err.message },
            ).message,
          )
          return cmdId
        }
        activeUpTasks.set(wsId, taskId)

        // A newer submission may already own the entry and must stay cancellable.
        const releaseTask = () => {
          if (activeUpTasks.get(wsId) === taskId) {
            activeUpTasks.delete(wsId)
          }
        }

        let firstProgress = true
        let signalledDone = false
        let suppressCallbacks = false
        let child: import("node:child_process").ChildProcess
        try {
          child = await cli.runStreaming(
            ["workspace", "task", "logs", taskId, "--follow"],
            (line, stream) => {
              if (
                signalledDone ||
                suppressCallbacks ||
                !workspaceJobs.owns(wsId, cmdId)
              )
                return

              if (firstProgress) {
                timing("first-progress")
                firstProgress = false
              }
              // Structured NDJSON envelopes only ever appear on stdout; stderr
              // carries freeform zap log lines.
              const envelope =
                stream === "stdout" ? parseCliEnvelope(line) : undefined

              if (envelope?.kind === "status") {
                const status = redactOperationStatus(
                  normalizeOperationStatus(envelope),
                )
                workspaceJobs.progress(wsId, cmdId, status)
                deps.getMainWindow()?.webContents.send("workspace-status", {
                  commandId: cmdId,
                  workspaceId: wsId,
                  ...status,
                })
                return
              }

              const formatted = formatLogLine(line)

              if (envelope?.kind === "result") {
                signalledDone = true
                releaseTask()
                timing("completed")
                void workspaceJobs.finish(wsId, generation)
                void sink.done(formatted, { success: true })
                return
              }

              if (envelope?.kind === "error") {
                signalledDone = true
                releaseTask()
                void workspaceJobs.finish(
                  wsId,
                  generation,
                  redactCLIError({
                    code: envelope.code ?? "up_failed",
                    message: envelope.message,
                  }).message,
                )
                void sink.done(formatted, {
                  level: "error",
                  success: false,
                  cliError: {
                    code: envelope.code ?? "up_failed",
                    message: envelope.message,
                    hint: envelope.hint,
                    context: envelope.context,
                  },
                })
                return
              }

              if (!sink.line(formatted)) return logStore.onDrain(logPath)
            },
            (code, cliError) => {
              // No releaseTask: the follower dying says nothing about the
              // detached worker, and would orphan a still-running task.
              if (tunnelProcesses.get(wsId) === child) {
                tunnelProcesses.delete(wsId)
              }
              if (
                signalledDone ||
                suppressCallbacks ||
                !workspaceJobs.owns(wsId, cmdId)
              )
                return
              // A follower exiting is not evidence that the detached task finished.
              void reconcileDetachedTask(
                wsId,
                cmdId,
                generation,
                taskId,
                sink,
                releaseTask,
              )
            },
            wsId,
          )
          // Expose a method to suppress callbacks from cancelActiveUp
          ;(
            child as unknown as { _suppressWorkspaceCallbacks?: () => void }
          )._suppressWorkspaceCallbacks = () => {
            suppressCallbacks = true
          }
        } catch (error) {
          // A failed follower does not prove that the detached task failed.
          // Keep its cancellation handle and reconcile from persisted task state.
          void reconcileDetachedTask(
            wsId,
            cmdId,
            generation,
            taskId,
            sink,
            releaseTask,
          )
          return cmdId
        }
        tunnelProcesses.set(wsId, child)

        return cmdId
      })
    })().catch(async (error) => {
      await workspaceJobs.finish(
        wsId,
        generation,
        redactCLIError(cliErrorOrFallback(error, "up_failed")).message,
      )
    })
    return { commandId: cmdId, completion }
  }

  ipcMain.handle(
    "workspace_up",
    (_event, args: Parameters<typeof runWorkspaceUp>[0]) =>
      runWorkspaceUp(args).commandId,
  )

  async function reconcileDetachedTask(
    wsId: string,
    commandId: string,
    generation: number,
    taskId: string,
    sink: ProgressSink,
    release: () => void,
  ): Promise<void> {
    while (
      workspaceJobs.owns(wsId, commandId) &&
      workspaceJobs.get(wsId)?.state === "running"
    ) {
      try {
        const raw = await cli.runRaw([
          "workspace",
          "task",
          "get",
          taskId,
          "--result-format",
          "json",
        ])
        if (!workspaceJobs.owns(wsId, commandId)) return
        for (const line of raw.split("\n")) {
          const envelope = parseCliEnvelope(line)
          if (envelope?.kind === "result") {
            release()
            await sink
              .done(formatLogLine("Completed"), { success: true })
              .catch((error) =>
                console.warn("[workspace-operation] log flush failed", error),
              )
            await workspaceJobs.finish(wsId, generation)
            return
          }
          if (envelope?.kind === "status")
            workspaceJobs.progress(
              wsId,
              commandId,
              redactOperationStatus(normalizeOperationStatus(envelope)),
            )
        }
      } catch (error) {
        // task get can also fail because the store/CLI is unavailable. Only a
        // persisted terminal task state authorizes declaring the worker failed.
        try {
          const tasks = await cli.run<
            Array<{
              id: string
              status: string
              error?: string
              errorCode?: string
            }>
          >(["workspace", "task", "list"])
          const task = tasks.find((task) => task.id === taskId)
          if (
            task?.status === "failed" &&
            workspaceJobs.owns(wsId, commandId)
          ) {
            release()
            const safe = redactCLIError({
              code: task.errorCode ?? "up_failed",
              message: task.error || "Workspace task failed",
            })
            await sink.done(formatLogLine(safe.message, "ERROR"), {
              success: false,
              cliError: safe,
            })
            await workspaceJobs.finish(wsId, generation, safe.message)
            return
          }
        } catch {
          /* Keep the operation and cancellation handle while unavailable. */
        }
        workspaceJobs.phase(
          wsId,
          commandId,
          "Progress unavailable · Checking task",
        )
      }
      await new Promise((resolve) => setTimeout(resolve, 3000))
    }
  }

  type WorkspaceActionSource = "renderer" | "tray"
  type StopWorkspaceArgs = {
    workspaceId: string
    debug?: boolean
    commandId?: string
  }

  function startWorkspaceAction(
    args: StopWorkspaceArgs,
    activity: WorkspaceActivity,
    cliArgs: string[],
    source: WorkspaceActionSource = "renderer",
  ): { commandId: string; completion: Promise<void> } {
    const commandId = args.commandId ?? crypto.randomUUID()
    const generation = workspaceJobs.start(
      args.workspaceId,
      activity,
      commandId,
    )
    const started = performance.now()
    const timing = (stage: string) =>
      console.debug(
        `[workspace-operation] ${commandId} ${stage} ${Math.round(performance.now() - started)}ms`,
      )
    trackEvent(
      `workspace_${{ stopping: "stop", deleting: "delete", rebuilding: "rebuild", resetting: "reset", starting: "up", creating: "create" }[activity]}`,
      { workspace_ref: hashWorkspaceRef(args.workspaceId), source },
    )
    timing("accepted")
    const completion = (async () => {
      let sink: ProgressSink | undefined
      try {
        workspaceJobs.phase(args.workspaceId, commandId, "Closing connections")
        await quiesceWorkspace(args.workspaceId)
        timing("shutdown-complete")
        const logPath = logStore.createLogFile(
          state.workspaceContext(args.workspaceId),
          args.workspaceId,
        )
        sink = createLogSink(
          deps.getMainWindow,
          commandId,
          (line) => logStore.appendLog(logPath, line),
          () => logStore.closeLog(logPath),
        )
        if (args.debug) cliArgs.push("--debug")
        workspaceJobs.phase(args.workspaceId, commandId, "Launching command")
        timing("cli-launch")
        let firstProgress = true
        await new Promise<void>((resolve, reject) => {
          void cli
            .runStreaming(
              cliArgs,
              (line, stream) => {
                if (!workspaceJobs.owns(args.workspaceId, commandId)) return
                if (firstProgress) {
                  timing("first-progress")
                  firstProgress = false
                }
                if (
                  forwardWorkspaceStatus(
                    commandId,
                    args.workspaceId,
                    line,
                    stream,
                  )
                )
                  return
                if (!sink!.line(formatLogLine(line)))
                  return logStore.onDrain(logPath)
              },
              (code, cliError) => {
                if (code === 0) resolve()
                else
                  reject(
                    Object.assign(
                      new Error(
                        cliError?.message ?? `Command exited with code ${code}`,
                      ),
                      { cliError },
                    ),
                  )
              },
              args.workspaceId,
            )
            .catch(reject)
        })
        timing("completed")
        await sink
          .done(formatLogLine("Completed"), { success: true })
          .catch((error) =>
            console.warn("[workspace-operation] log flush failed", error),
          )
        await workspaceJobs.finish(args.workspaceId, generation)
        timing("reconciled")
      } catch (error) {
        const cliError = redactCLIError(
          cliErrorOrFallback(error, "workspace_operation_failed"),
        )
        if (sink)
          await sink
            .done(formatLogLine(cliError.message, "ERROR"), {
              success: false,
              level: "error",
              cliError,
            })
            .catch(() => {})
        await workspaceJobs.finish(
          args.workspaceId,
          generation,
          cliError.message,
        )
        throw error
      }
    })()
    // IPC acknowledges acceptance. All subsequent errors live in the shared job.
    void completion.catch(() => {})
    return { commandId, completion }
  }

  function startWorkspaceStop(
    args: StopWorkspaceArgs,
    source: WorkspaceActionSource,
  ) {
    return startWorkspaceAction(
      args,
      "stopping",
      ["workspace", "stop", args.workspaceId],
      source,
    )
  }
  ipcMain.handle(
    "workspace_stop",
    (_event, args: StopWorkspaceArgs) =>
      startWorkspaceStop(args, "renderer").commandId,
  )
  ipcMain.handle(
    "workspace_delete",
    (_event, args: StopWorkspaceArgs) =>
      startWorkspaceAction(args, "deleting", [
        "workspace",
        "delete",
        args.workspaceId,
        "--force",
      ]).commandId,
  )
  ipcMain.handle(
    "workspace_rebuild",
    (_event, args: StopWorkspaceArgs) =>
      startWorkspaceAction(args, "rebuilding", [
        "workspace",
        "up",
        args.workspaceId,
        "--recreate",
      ]).commandId,
  )
  ipcMain.handle(
    "workspace_reset",
    (_event, args: StopWorkspaceArgs) =>
      startWorkspaceAction(args, "resetting", [
        "workspace",
        "up",
        args.workspaceId,
        "--reset",
      ]).commandId,
  )

  // ── Terminal ──
  ipcMain.handle(
    "terminal_create",
    async (_event, args: { cols: number; rows: number }) => {
      return deps.pty.createSession(args.cols, args.rows)
    },
  )

  ipcMain.handle(
    "terminal_create_ssh",
    async (
      _event,
      args: { workspaceId: string; cols: number; rows: number },
    ) => {
      return deps.pty.createSshSession(args.workspaceId, args.cols, args.rows)
    },
  )

  ipcMain.handle(
    "terminal_write",
    async (_event, args: { sessionId: string; data: number[] }) => {
      const text = new TextDecoder().decode(new Uint8Array(args.data))
      deps.pty.writeToSession(args.sessionId, text)
    },
  )

  ipcMain.handle(
    "terminal_resize",
    async (_event, args: { sessionId: string; cols: number; rows: number }) => {
      deps.pty.resizeSession(args.sessionId, args.cols, args.rows)
    },
  )

  ipcMain.handle(
    "terminal_close",
    async (_event, args: { sessionId: string }) => {
      deps.pty.closeSession(args.sessionId)
    },
  )

  ipcMain.handle("terminal_list", async () => {
    return deps.pty.listSessions()
  })

  // ── SSH Keys ──
  ipcMain.handle("ssh_key_list", async () => {
    const sshDir = join(homedir(), ".ssh")
    if (!existsSync(sshDir)) return []

    const entries = await readdir(sshDir)
    const pubFiles = entries.filter((f) => f.endsWith(".pub"))
    const keys: SshKeyInfo[] = []

    for (const pubFile of pubFiles) {
      const pubPath = join(sshDir, pubFile)
      let pubContent: string
      try {
        pubContent = (await readFile(pubPath, "utf-8")).trim()
      } catch {
        continue
      }

      const parts = pubContent.split(/\s+/, 3)
      if (parts.length < 2) continue

      const keyType = parts[0]
      const comment = parts[2] ?? ""
      const baseName = pubFile.replace(/\.pub$/, "")
      const privatePath = join(sshDir, baseName)

      // Get fingerprint via ssh-keygen
      let fingerprint = ""
      try {
        const { stdout } = await execFileAsync("ssh-keygen", [
          "-l",
          "-f",
          pubPath,
        ])
        fingerprint = stdout.trim()
      } catch {
        // ssh-keygen not available or key unreadable
      }

      // Check if private key has passphrase
      let hasPassphrase = false
      if (existsSync(privatePath)) {
        try {
          const { status } = await new Promise<{ status: number | null }>(
            (resolve) => {
              const proc = execFile(
                "ssh-keygen",
                ["-y", "-P", "", "-f", privatePath],
                () => {},
              )
              proc.on("close", (status) => resolve({ status }))
            },
          )
          hasPassphrase = status !== 0
        } catch {
          hasPassphrase = false
        }
      }

      keys.push({
        name: baseName,
        keyType,
        fingerprint,
        comment,
        publicKey: pubContent,
        path: privatePath,
        hasPassphrase,
      })
    }

    keys.sort((a, b) => a.name.localeCompare(b.name))
    return keys
  })

  ipcMain.handle(
    "ssh_key_generate",
    async (
      _event,
      args: { name: string; keyType?: string; comment?: string },
    ) => {
      const sshDir = join(homedir(), ".ssh")
      await mkdir(sshDir, { recursive: true, mode: 0o700 })

      const keyPath = join(sshDir, args.name)
      if (existsSync(keyPath)) {
        throw new Error(`Key '${args.name}' already exists`)
      }

      const algo = args.keyType ?? "ed25519"
      const cmt = args.comment ?? `devsy-${args.name}`

      await execFileAsync("ssh-keygen", [
        "-t",
        algo,
        "-C",
        cmt,
        "-N",
        "",
        "-f",
        keyPath,
      ])

      // Read the generated public key
      const pubPath = `${keyPath}.pub`
      const pubContent = (await readFile(pubPath, "utf-8")).trim()
      const parts = pubContent.split(/\s+/, 3)
      const keyType = parts[0] ?? ""

      // Get fingerprint
      let fingerprint = ""
      try {
        const { stdout } = await execFileAsync("ssh-keygen", [
          "-l",
          "-f",
          pubPath,
        ])
        fingerprint = stdout.trim()
      } catch {
        // Ignore
      }

      const key: SshKeyInfo = {
        name: args.name,
        keyType,
        fingerprint,
        comment: cmt,
        publicKey: pubContent,
        path: keyPath,
        hasPassphrase: false,
      }

      return key
    },
  )

  // ── Release Channel ──
  ipcMain.handle("get_release_channel", () => {
    return getReleaseChannel()
  })

  ipcMain.handle(
    "set_release_channel",
    async (_event, args: { channel: string }) => {
      if (args.channel !== "stable" && args.channel !== "beta") {
        throw new Error(`Invalid release channel: ${args.channel}`)
      }
      const channel: ReleaseChannel = args.channel
      await switchReleaseChannel(channel)
    },
  )

  ipcMain.handle("check_for_updates", async () => {
    await checkForUpdates()
  })

  // Deferred so the renderer's update-status listener (registered after this
  // call resolves) is attached before the replay arrives.
  ipcMain.handle("app_ready", (event) => {
    deps.onRendererReady?.(event.sender)
    setImmediate(() => {
      if (!event.sender.isDestroyed()) {
        event.sender.send("update-status", getLastStatus())
      }
    })
  })

  ipcMain.handle("install_update", async () => {
    installUpdate()
  })

  ipcMain.handle("download_update", async () => {
    await downloadUpdate()
  })

  ipcMain.handle("get_app_version", () => {
    return app.getVersion()
  })

  ipcMain.handle("get_host_platform", () => {
    // Devcontainers always target Linux: Docker Desktop/Podman run containers
    // in a Linux VM, so the host process OS (darwin/win32) is never the
    // container target. Only the architecture varies by host.
    return `linux/${dockerArch(process.arch)}`
  })

  ipcMain.handle(
    "image_inspect_platforms",
    async (_event, args: { ref: string }) => {
      const result = await cli.run<{ platforms: string[] }>([
        "internal",
        "get-image-platforms",
        args.ref,
      ])
      return result.platforms
    },
  )

  ipcMain.handle("get_auto_download", () => {
    return getAutoDownloadEnabled()
  })

  ipcMain.handle(
    "set_auto_download",
    async (_event, args: { enabled: boolean }) => {
      if (typeof args?.enabled !== "boolean") {
        throw new Error("enabled must be boolean")
      }
      setAutoDownloadEnabled(args.enabled)
    },
  )

  ipcMain.handle("get_app_settings", () => {
    if (!deps.settingsService) throw new Error("Settings service unavailable")
    return deps.settingsService.status()
  })

  ipcMain.handle(
    "set_app_settings",
    async (_event, args: { patch?: Record<string, unknown> }) => {
      if (!deps.settingsService) throw new Error("Settings service unavailable")
      return deps.settingsService.update(sanitizeAppSettingsPatch(args?.patch))
    },
  )

  // ── Analytics ──
  ipcMain.handle(
    "analytics_track",
    async (
      _event,
      args: { name: string; properties?: Record<string, unknown> },
    ) => {
      if (
        !args?.name ||
        typeof args.name !== "string" ||
        args.name.length > 64
      ) {
        return
      }
      trackEvent(args.name, sanitizeAnalyticsProperties(args.properties))
    },
  )

  function runUpdateCheck(): void {
    void (async () => {
      try {
        providerUpdateCache = await computeUpdateChecks()
        providerUpdateCacheCheckedAt = new Date().toISOString()
      } catch {
        // Silently swallow background errors.
      }
    })()
  }

  function scheduleUpdates(): void {
    setInterval(runUpdateCheck, 6 * 60 * 60 * 1000)
  }

  return {
    tunnelProcesses,
    scheduleProviderUpdateCheck: scheduleUpdates,
    runInitialProviderUpdateCheck: runUpdateCheck,
    workspaceActions: {
      async stop(workspaceId: string): Promise<void> {
        const { completion } = await startWorkspaceStop({ workspaceId }, "tray")
        await completion
      },
      async start(workspaceId: string): Promise<void> {
        await runWorkspaceUp({
          source: workspaceId,
          commandId: crypto.randomUUID(),
        }).completion
      },
    },
  }
}

function sanitizeAnalyticsProperties(
  input?: Record<string, unknown>,
): Record<string, unknown> | undefined {
  if (!input || typeof input !== "object") return undefined
  const entries = Object.entries(input).slice(0, 20)
  const out: Record<string, unknown> = {}
  for (const [k, v] of entries) {
    if (!k || k.length > 64) continue
    out[k] = typeof v === "string" ? v.slice(0, 256) : v
  }
  return out
}
