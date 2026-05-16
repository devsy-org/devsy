import { execFile } from "node:child_process"
import { existsSync } from "node:fs"
import { mkdir, readdir, readFile } from "node:fs/promises"
import { homedir } from "node:os"
import { join } from "node:path"
import { promisify } from "node:util"
import type { BrowserWindow } from "electron"
import { ipcMain } from "electron"
import { trackEvent } from "./analytics.js"
import type { CliRunner } from "./cli.js"
import type { LogStore } from "./log-store.js"
import type { PtyManager } from "./pty.js"
import type { DaemonState } from "./state.js"
import { type ProviderEntry, parseProviderEntries } from "./watcher.js"

const execFileAsync = promisify(execFile)

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
  pty: PtyManager
  getMainWindow: () => BrowserWindow | null
}

/** Format a line in zap console format so log-parser.ts can parse it. */
function formatLogLine(line: string, level: "INFO" | "ERROR" = "INFO"): string {
  return `${new Date().toISOString()}\t${level}\t${line}`
}

export function registerIpcHandlers(deps: IpcDependencies): void {
  const { cli, state, logStore } = deps

  // ── Workspaces ──
  ipcMain.handle("workspace_list", () => state.workspaceList())

  ipcMain.handle(
    "workspace_status",
    async (_event, args: { workspaceId: string }) => {
      return cli.runRaw([
        "status",
        args.workspaceId,
        "--output",
        "json",
        "--timeout",
        "5s",
      ])
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
    async (_event, args: { name: string; source?: string }) => {
      trackEvent("provider_add")
      const src = args.source ?? args.name
      const cliArgs = ["provider", "add", src, "--use=false"]
      if (args.source) {
        cliArgs.push("--name", args.name)
      }
      await cli.runRaw(cliArgs)
    },
  )

  ipcMain.handle("provider_delete", async (_event, args: { name: string }) => {
    trackEvent("provider_remove")
    await cli.runRaw(["provider", "delete", args.name])
  })

  ipcMain.handle("provider_use", async (_event, args: { name: string }) => {
    await cli.runRaw(["provider", "use", args.name])
  })

  ipcMain.handle("provider_init", async (_event, args: { name: string }) => {
    await cli.runRaw(["provider", "set-options", args.name])
  })

  ipcMain.handle("provider_update", async (_event, args: { name: string }) => {
    await cli.runRaw(["provider", "update", args.name, "--use=false"])
  })

  ipcMain.handle("provider_options", async (_event, args: { name: string }) => {
    return cli.run(["provider", "options", args.name])
  })

  ipcMain.handle(
    "provider_set_options",
    async (_event, args: { name: string; options: string[] }) => {
      const cliArgs = ["provider", "set-options", args.name]
      for (const opt of args.options) {
        cliArgs.push("-o", opt)
      }
      await cli.runRaw(cliArgs)
    },
  )

  ipcMain.handle(
    "provider_rename",
    async (_event, args: { name: string; newName: string }) => {
      await cli.runRaw(["provider", "rename", args.name, args.newName])
    },
  )

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
      const cliArgs = ["machine", "delete", args.id]
      if (args.force) cliArgs.push("--force")
      await cli.runRaw(cliArgs)
    },
  )

  ipcMain.handle("machine_start", async (_event, args: { id: string }) => {
    await cli.runRaw(["machine", "start", args.id])
  })

  ipcMain.handle("machine_stop", async (_event, args: { id: string }) => {
    await cli.runRaw(["machine", "stop", args.id])
  })

  ipcMain.handle("machine_status", async (_event, args: { id: string }) => {
    return cli.runRaw(["machine", "status", args.id, "--output", "json"])
  })

  // ── Contexts ──
  ipcMain.handle("context_list", () => state.contextList())

  ipcMain.handle("context_use", async (_event, args: { name: string }) => {
    await cli.runRaw(["context", "use", args.name])
  })

  ipcMain.handle(
    "context_options",
    async (_event, args: { context?: string }) => {
      const cliArgs = ["context", "options"]
      if (args.context) cliArgs.push("--context", args.context)
      return cli.run(cliArgs)
    },
  )

  ipcMain.handle(
    "context_set_options",
    async (_event, args: { options: string[]; context?: string }) => {
      const cliArgs: string[] = ["context", "set-options"]
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

  // ── System ──
  ipcMain.handle("devsy_version", async () => {
    return cli.runRaw(["version"])
  })

  ipcMain.handle(
    "devsy_upgrade",
    async (_event, args: { version: string }) => {
      return cli.runRaw(["upgrade", "--version", args.version])
    },
  )

  ipcMain.handle(
    "devsy_upgrade_dry_run",
    async (_event, args: { version: string }) => {
      return cli.runRaw(["upgrade", "--version", args.version, "--dry-run"])
    },
  )

  // ── Logs ──
  ipcMain.handle(
    "workspace_logs_list",
    async (_event, args: { workspaceId: string }) => {
      return logStore.listLogs(args.workspaceId)
    },
  )

  ipcMain.handle(
    "workspace_log_read",
    async (_event, args: { workspaceId: string; filename: string }) => {
      return logStore.readLog(args.workspaceId, args.filename)
    },
  )

  ipcMain.handle(
    "workspace_log_delete",
    async (_event, args: { workspaceId: string; filename: string }) => {
      logStore.deleteLog(args.workspaceId, args.filename)
    },
  )

  ipcMain.handle(
    "workspace_up",
    async (
      _event,
      args: {
        source: string
        workspaceId?: string
        provider?: string
        ide?: string
        debug?: boolean
      },
    ) => {
      trackEvent("workspace_create", { provider: args.provider })
      const cliArgs = ["up", args.source]
      if (args.workspaceId) cliArgs.push("--id", args.workspaceId)
      if (args.provider) cliArgs.push("--provider", args.provider)
      if (args.ide) cliArgs.push("--ide", args.ide)
      if (args.debug) cliArgs.push("--debug")

      const wsId = args.workspaceId ?? args.source
      const cmdId = crypto.randomUUID()
      const logPath = logStore.createLogFile(wsId)
      const win = deps.getMainWindow()

      cli.runStreaming(
        cliArgs,
        (line) => {
          const formatted = formatLogLine(line)
          logStore.appendLog(logPath, formatted)
          win?.webContents.send("command-progress", {
            commandId: cmdId,
            message: formatted,
            done: false,
          })
        },
        (code) => {
          const exitMsg = formatLogLine(
            `Exit code: ${code}`,
            code === 0 ? "INFO" : "ERROR",
          )
          logStore.appendLog(logPath, exitMsg)
          win?.webContents.send("command-progress", {
            commandId: cmdId,
            message: exitMsg,
            done: true,
          })
        },
      )

      return cmdId
    },
  )

  ipcMain.handle(
    "workspace_stop",
    async (_event, args: { workspaceId: string; debug?: boolean }) => {
      trackEvent("workspace_stop")
      const cmdId = crypto.randomUUID()
      const logPath = logStore.createLogFile(args.workspaceId)
      const win = deps.getMainWindow()

      const cliArgs = ["stop", args.workspaceId]
      if (args.debug) cliArgs.push("--debug")

      cli.runStreaming(
        cliArgs,
        (line) => {
          const formatted = formatLogLine(line)
          logStore.appendLog(logPath, formatted)
          win?.webContents.send("command-progress", {
            commandId: cmdId,
            message: formatted,
            done: false,
          })
        },
        (code) => {
          const exitMsg = formatLogLine(
            `Exit code: ${code}`,
            code === 0 ? "INFO" : "ERROR",
          )
          logStore.appendLog(logPath, exitMsg)
          win?.webContents.send("command-progress", {
            commandId: cmdId,
            message: exitMsg,
            done: true,
          })
        },
      )

      return cmdId
    },
  )

  ipcMain.handle(
    "workspace_delete",
    async (_event, args: { workspaceId: string; debug?: boolean }) => {
      trackEvent("workspace_delete")
      const cmdId = crypto.randomUUID()
      const logPath = logStore.createLogFile(args.workspaceId)
      const win = deps.getMainWindow()

      const cliArgs = ["delete", args.workspaceId]
      if (args.debug) cliArgs.push("--debug")
      cliArgs.push("--force")

      cli.runStreaming(
        cliArgs,
        (line) => {
          const formatted = formatLogLine(line)
          logStore.appendLog(logPath, formatted)
          win?.webContents.send("command-progress", {
            commandId: cmdId,
            message: formatted,
            done: false,
          })
        },
        (code) => {
          const exitMsg = formatLogLine(
            `Exit code: ${code}`,
            code === 0 ? "INFO" : "ERROR",
          )
          logStore.appendLog(logPath, exitMsg)
          win?.webContents.send("command-progress", {
            commandId: cmdId,
            message: exitMsg,
            done: true,
          })
        },
      )

      return cmdId
    },
  )

  ipcMain.handle(
    "workspace_rebuild",
    async (_event, args: { workspaceId: string; debug?: boolean }) => {
      trackEvent("workspace_rebuild")
      const cmdId = crypto.randomUUID()
      const logPath = logStore.createLogFile(args.workspaceId)
      const win = deps.getMainWindow()

      const cliArgs = ["up", args.workspaceId, "--recreate"]
      if (args.debug) cliArgs.push("--debug")

      cli.runStreaming(
        cliArgs,
        (line) => {
          const formatted = formatLogLine(line)
          logStore.appendLog(logPath, formatted)
          win?.webContents.send("command-progress", {
            commandId: cmdId,
            message: formatted,
            done: false,
          })
        },
        (code) => {
          const exitMsg = formatLogLine(
            `Exit code: ${code}`,
            code === 0 ? "INFO" : "ERROR",
          )
          logStore.appendLog(logPath, exitMsg)
          win?.webContents.send("command-progress", {
            commandId: cmdId,
            message: exitMsg,
            done: true,
          })
        },
      )

      return cmdId
    },
  )

  ipcMain.handle(
    "workspace_reset",
    async (_event, args: { workspaceId: string; debug?: boolean }) => {
      trackEvent("workspace_reset")
      const cmdId = crypto.randomUUID()
      const logPath = logStore.createLogFile(args.workspaceId)
      const win = deps.getMainWindow()

      const cliArgs = ["up", args.workspaceId, "--reset"]
      if (args.debug) cliArgs.push("--debug")

      cli.runStreaming(
        cliArgs,
        (line) => {
          const formatted = formatLogLine(line)
          logStore.appendLog(logPath, formatted)
          win?.webContents.send("command-progress", {
            commandId: cmdId,
            message: formatted,
            done: false,
          })
        },
        (code) => {
          const exitMsg = formatLogLine(
            `Exit code: ${code}`,
            code === 0 ? "INFO" : "ERROR",
          )
          logStore.appendLog(logPath, exitMsg)
          win?.webContents.send("command-progress", {
            commandId: cmdId,
            message: exitMsg,
            done: true,
          })
        },
      )

      return cmdId
    },
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
