<script lang="ts">
import { goto, querystring } from "$lib/router.js"
import { onMount, onDestroy } from "svelte"
import {
  Check,
  ChevronsUpDown,
  ClipboardCopy,
  Ellipsis,
  LifeBuoy,
  Monitor,
  Pencil,
  Play,
  RefreshCw,
  RotateCcw,
  ScrollText,
  Square,
  SquareTerminal,
  Trash2,
} from "@lucide/svelte"
import * as Tooltip from "$lib/components/ui/tooltip/index.js"
import { Spinner } from "$lib/components/ui/spinner/index.js"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import * as ButtonGroup from "$lib/components/ui/button-group/index.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import * as Command from "$lib/components/ui/command/index.js"
import * as Popover from "$lib/components/ui/popover/index.js"
import * as DropdownMenu from "$lib/components/ui/dropdown-menu/index.js"
import * as Accordion from "$lib/components/ui/accordion/index.js"
import * as Tabs from "$lib/components/ui/tabs/index.js"
import ConfirmDialog from "$lib/components/layout/ConfirmDialog.svelte"
import LogTable from "$lib/components/log/LogTable.svelte"
import TerminalComponent from "$lib/components/terminal/Terminal.svelte"
import { workspaces } from "$lib/stores/workspaces.js"
import { addTerminal, removeTerminal } from "$lib/stores/terminals.js"
import { destroyTerminalInstance } from "$lib/stores/terminal-instances.js"
import { terminalCreateSsh, terminalClose } from "$lib/ipc/terminal.js"
import {
  workspaceUp,
  workspaceStop,
  workspaceRebuild,
  workspaceReset,
  workspaceDelete,
  workspaceRename,
  workspaceSetIde,
  workspaceStatus,
  workspaceLogsList,
  workspaceLogRead,
  workspaceLogDelete,
} from "$lib/ipc/commands.js"
import { onCommandProgress } from "$lib/ipc/events.js"
import {
  loadLocalOptions,
  getWorkspaceFolder,
  setWorkspaceFolder,
  getWorkspaceRecoveryState,
  setWorkspaceRecoveryState,
} from "$lib/stores/settings.js"
import { toasts } from "$lib/stores/toasts.js"
import { extractErrorMessage } from "$lib/utils/error.js"
import { trackEngagement } from "$lib/analytics.js"
import type { CommandProgress, LogEntry } from "$lib/types/index.js"
import type { UnlistenFn } from "$lib/ipc/types.js"
import { formatTimestamp } from "$lib/utils/time.js"
import {
  isRecoverableBuildFailure,
  isCommandSuccess,
  parseRecoveryContainer,
  stripAnsi,
} from "$lib/utils/log-parser.js"
import { Skeleton } from "$lib/components/ui/skeleton/index.js"

let { params = {} }: { params?: Record<string, string> } = $props()

// Segmented pill tabs: the active tab reads as a raised solid pill (elevated
// background, primary text, semibold) against the muted track.
const tabTriggerClass =
  "px-4 text-muted-foreground data-active:text-primary data-active:font-semibold"

const IDE_OPTIONS = [
  { value: "none", label: "None" },
  { value: "vscode", label: "VS Code" },
  { value: "openvscode", label: "OpenVSCode Server" },
  { value: "vscode-web", label: "VS Code for the Web" },
  { value: "code-server", label: "code-server" },
  { value: "cursor", label: "Cursor" },
  { value: "zed", label: "Zed" },
  { value: "codium", label: "VSCodium" },
  { value: "windsurf", label: "Windsurf Editor" },
  { value: "antigravity", label: "Google Antigravity" },
  { value: "bob", label: "IBM Bob" },
  { value: "intellij", label: "IntelliJ IDEA" },
  { value: "pycharm", label: "PyCharm" },
  { value: "phpstorm", label: "PhpStorm" },
  { value: "rider", label: "Rider" },
  { value: "fleet", label: "Fleet" },
  { value: "goland", label: "GoLand" },
  { value: "webstorm", label: "WebStorm" },
  { value: "rustrover", label: "RustRover" },
  { value: "rubymine", label: "RubyMine" },
  { value: "clion", label: "CLion" },
  { value: "dataspell", label: "DataSpell" },
  { value: "jupyternotebook", label: "Jupyter Notebook" },
  { value: "marimo", label: "marimo" },
  { value: "vscode-insiders", label: "VS Code Insiders" },
  { value: "positron", label: "Positron" },
  { value: "rstudio", label: "RStudio Server" },
]

let id = $derived(params.id ?? "")
let workspace = $derived($workspaces.find((ws) => ws.id === id))
// ".devsy" matches config.SSHHostSuffix on the backend (".<binary-name>").
let sshHost = $derived(`${id}.devsy`)

let isRunning = $derived(workspace?.status?.toLowerCase() === "running")
let isStopped = $derived(
  !workspace?.status ||
    workspace.status.toLowerCase() === "stopped" ||
    workspace.status.toLowerCase() === "notfound",
)
let isBusy = $derived.by(() => {
  const status = workspace?.status?.toLowerCase()
  return (
    status === "busy" ||
    status === "starting" ||
    status === "stopping" ||
    status === "deleting"
  )
})

function statusBadgeVariant(): "default" | "secondary" | "outline" {
  if (isRunning) return "default"
  if (isBusy) return "secondary"
  return "outline"
}

function setWorkspaceStatus(status?: string) {
  if (!id) return
  workspaces.update((current) =>
    current.map((ws) => (ws.id === id ? { ...ws, status } : ws)),
  )
}

const BUILD_OPS = new Set(["Start", "Open IDE", "Recovery", "Rebuild", "Reset"])

let activeTab = $state("overview")
let outputLines = $state<string[]>([])
let commandId = $state<string | null>(null)
let operationLabel = $state("")
let operationRunning = $state(false)
let lastOperationStatus: string | undefined = undefined
let buildFailed = $state(false)
let inRecovery = $state(false)
// True only for a buildFailed loaded from persistence, so the reconciliation
// clears stale banners from external rebuilds without touching in-app failures.
let staleReconcilePending = $state(false)

function persistRecovery() {
  setWorkspaceRecoveryState(id, { buildFailed, inRecovery })
}

// Reconcile inRecovery against the container's persisted recovery state (set by
// the last up, in-app or external), so a rebuild done outside the app is reflected.
async function reconcileRecoveryFromStatus() {
  try {
    const status = JSON.parse(await workspaceStatus(id, true)) as {
      state?: string
      recovery?: boolean
    }
    if (status.state === "Running") {
      inRecovery = status.recovery === true
      persistRecovery()
    }
  } catch {
    // status unavailable; keep persisted state
  }
}

$effect(() => {
  if (staleReconcilePending && isRunning && !operationRunning) {
    staleReconcilePending = false
    buildFailed = false
    persistRecovery()
  }
})
let unlisten: UnlistenFn | null = null
let pendingLines: string[] = []
let flushHandle: number | null = null

let logEntries = $state<LogEntry[]>([])
let selectedLog = $state<string | null>(null)
let logContent = $state<string>("")
let logsLoading = $state(false)

let confirmDeleteOpen = $state(false)
let deleting = $state(false)
let confirmRenameOpen = $state(false)
let pendingRenameTarget = $state("")
let confirmRebuildOpen = $state(false)
let confirmResetOpen = $state(false)

let hasContainer = $derived.by(() => {
  const status = workspace?.status?.toLowerCase()
  return Boolean(status) && status !== "notfound"
})

let sshSessionId = $state<string | null>(null)
let sshExited = $state(false)
let sshConnectionFailed = $state(false)
let connecting = $state(false)
let ideComboOpen = $state(false)
let ideSearch = $state("")
let selectedIde = $state<string | null>(null)
let ideSetSeq = 0
let renaming = $state(false)
let renameValue = $state("")
let renameSaving = $state(false)
let editingFolder = $state(false)
let folderValue = $state("")
let customFolder = $state("")
let currentIde = $derived(selectedIde ?? workspace?.ide?.name ?? "none")
let filteredIdes = $derived(
  IDE_OPTIONS.filter((i) =>
    i.label.toLowerCase().includes(ideSearch.toLowerCase()),
  ),
)

const MAX_LOG_LINES = 5000
const TRIM_TARGET = 4000

function flushLines() {
  flushHandle = null
  if (pendingLines.length === 0) return
  outputLines.push(...pendingLines)
  pendingLines.length = 0
  if (outputLines.length > MAX_LOG_LINES) {
    outputLines.splice(0, outputLines.length - TRIM_TARGET)
  }
}

async function copyToClipboard(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    toasts.success("Copied to clipboard")
  } catch {
    toasts.error("Failed to copy to clipboard")
  }
}

onMount(async () => {
  try {
    unlisten = await onCommandProgress((progress) => {
      if (commandId && progress.commandId === commandId) {
        const incoming =
          progress.lines ?? (progress.message ? [progress.message] : [])
        if (incoming.length > 0) {
          pendingLines.push(...incoming)
          if (flushHandle === null) {
            flushHandle = requestAnimationFrame(flushLines)
          }
        }
        if (progress.done) {
          if (flushHandle !== null) {
            cancelAnimationFrame(flushHandle)
          }
          flushLines()
          operationRunning = false
          const success = isCommandSuccess(progress.message, progress.success)
          if (success) {
            toasts.success(`${operationLabel} ${id} succeeded`)
            if (BUILD_OPS.has(operationLabel)) {
              buildFailed = false
              const recovered = parseRecoveryContainer(progress.message)
              inRecovery = recovered ?? operationLabel === "Recovery"
              persistRecovery()
            }
          } else {
            toasts.error(
              `${operationLabel} ${id} failed. Check output for details.`,
            )
            handleBuildFailure(progress)
          }
          if (operationLabel === "Delete" && success) {
            goto("/workspaces")
            return
          }
          loadLogs()
        }
      }
    })
  } catch {
    // Event listener setup failed
  }

  loadLogs()
  customFolder = getWorkspaceFolder(id)
  const rec = getWorkspaceRecoveryState(id)
  buildFailed = rec.buildFailed ?? false
  inRecovery = rec.inRecovery ?? false
  staleReconcilePending = buildFailed
  void reconcileRecoveryFromStatus()

  const qs = new URLSearchParams($querystring ?? "")
  const action = qs.get("action")
  if (action === "open-ide" || action === "start") {
    // Clear query param so refresh doesn't re-trigger
    history.replaceState(
      {},
      "",
      window.location.pathname + window.location.hash.split("?")[0],
    )
    if (action === "open-ide") {
      handleOpenIde()
    } else {
      handleStart()
    }
  }
})

onDestroy(() => {
  unlisten?.()
  if (flushHandle !== null) {
    cancelAnimationFrame(flushHandle)
    flushHandle = null
  }
  // Clean up SSH session if navigating away
  if (sshSessionId) {
    if (!sshExited) {
      terminalClose(sshSessionId).catch(() => {})
    }
    destroyTerminalInstance(sshSessionId)
    removeTerminal(sshSessionId)
  }
})

async function loadLogs() {
  logsLoading = true
  try {
    logEntries = await workspaceLogsList(id)
  } catch {
    logEntries = []
  } finally {
    logsLoading = false
  }
}

async function viewLog(entry: LogEntry) {
  selectedLog = entry.filename
  try {
    logContent = await workspaceLogRead(id, entry.filename)
  } catch {
    logContent = "Failed to load log content."
  }
}

async function deleteLog(entry: LogEntry) {
  try {
    await workspaceLogDelete(id, entry.filename)
    logEntries = logEntries.filter((e) => e.filename !== entry.filename)
    if (selectedLog === entry.filename) {
      selectedLog = null
      logContent = ""
    }
    toasts.success("Log file deleted")
  } catch (err) {
    toasts.error(`Failed to delete log: ${extractErrorMessage(err)}`)
  }
}

async function handleConnect() {
  connecting = true
  sshSessionId = null
  sshExited = false
  sshConnectionFailed = false
  try {
    const sessionId = await terminalCreateSsh(id, 80, 24)
    sshSessionId = sessionId
    addTerminal({
      id: sessionId,
      label: `SSH: ${id}`,
      type: "ssh",
      workspaceId: id,
    })
    trackEngagement("terminal_open", { type: "ssh" })
    activeTab = "terminal"
    toasts.success(`Connected to ${id}`)
  } catch (err) {
    toasts.error(`Failed to connect: ${extractErrorMessage(err)}`)
  } finally {
    connecting = false
  }
}

async function handleDisconnect() {
  if (!sshSessionId) return
  if (!sshExited) {
    try {
      await terminalClose(sshSessionId)
    } catch {
      // session may already be gone
    }
  }
  destroyTerminalInstance(sshSessionId)
  removeTerminal(sshSessionId)
  sshSessionId = null
  sshExited = false
  sshConnectionFailed = false
}

function handleSshExit(exitCode?: number, _signal?: number) {
  if (sshSessionId) {
    sshExited = true
    if (exitCode === -1) {
      sshConnectionFailed = true
    }
  }
}

function handleGpgForwardFailed(reason: string) {
  toasts.error(
    `GPG agent forwarding failed for ${id}, continuing without it: ${reason}`,
  )
}

function isDebug(): boolean {
  return loadLocalOptions().debugFlag
}

function startStreamingOp(label: string, pendingStatus?: string) {
  operationLabel = label
  operationRunning = true
  lastOperationStatus = workspace?.status
  buildFailed = false
  persistRecovery()
  outputLines = []
  pendingLines = []
  if (flushHandle !== null) {
    cancelAnimationFrame(flushHandle)
    flushHandle = null
  }
  if (pendingStatus) {
    setWorkspaceStatus(pendingStatus)
  }
  activeTab = "logs"
}

async function handleStart() {
  const ide = currentIde
  const folder = customFolder || undefined
  const previousStatus = workspace?.status
  startStreamingOp("Start", "starting")
  try {
    commandId = await workspaceUp({
      source: id,
      ide,
      debug: isDebug(),
      workspaceFolder: folder,
    })
  } catch (err) {
    operationRunning = false
    setWorkspaceStatus(previousStatus)
    toasts.error(`Failed to start: ${extractErrorMessage(err)}`)
  }
}

function handleBuildFailure(progress: CommandProgress) {
  if (
    !BUILD_OPS.has(operationLabel) ||
    !isRecoverableBuildFailure(progress.cliError)
  ) {
    return
  }
  const pref = loadLocalOptions().onBuildFailure
  if (pref === "nothing") return
  // Avoid a loop: don't auto-retry recovery when recovery itself failed.
  if (pref === "auto-recovery" && operationLabel !== "Recovery") {
    void handleRecovery()
    return
  }
  buildFailed = true
  inRecovery = false
  persistRecovery()
}

async function handleRecovery() {
  const ide = currentIde
  const folder = customFolder || undefined
  const previousStatus = workspace?.status
  startStreamingOp("Recovery", "busy")
  try {
    commandId = await workspaceUp({
      source: id,
      ide,
      recovery: true,
      debug: isDebug(),
      workspaceFolder: folder,
    })
  } catch (err) {
    operationRunning = false
    setWorkspaceStatus(previousStatus)
    toasts.error(
      `Failed to start recovery container: ${extractErrorMessage(err)}`,
    )
  }
}

async function handleOpenIde() {
  const ide = currentIde
  const folder = customFolder || undefined
  const previousStatus = workspace?.status
  trackEngagement("ide_open", { ide })
  startStreamingOp("Open IDE", "busy")
  try {
    commandId = await workspaceUp({
      source: id,
      ide,
      ideLaunch: "auto",
      debug: isDebug(),
      workspaceFolder: folder,
    })
  } catch (err) {
    operationRunning = false
    setWorkspaceStatus(previousStatus)
    toasts.error(`Failed to open IDE: ${extractErrorMessage(err)}`)
  }
}

async function handleStop() {
  const previousStatus = workspace?.status
  startStreamingOp("Stop", "stopping")
  try {
    commandId = await workspaceStop(id, isDebug())
  } catch (err) {
    operationRunning = false
    setWorkspaceStatus(previousStatus)
    toasts.error(`Failed to stop: ${extractErrorMessage(err)}`)
  }
}

async function handleRebuild() {
  confirmRebuildOpen = false
  const previousStatus = workspace?.status
  startStreamingOp("Rebuild", "busy")
  try {
    commandId = await workspaceRebuild(id, isDebug())
  } catch (err) {
    operationRunning = false
    setWorkspaceStatus(previousStatus)
    toasts.error(`Failed to rebuild: ${extractErrorMessage(err)}`)
  }
}

async function handleReset() {
  confirmResetOpen = false
  const previousStatus = workspace?.status
  startStreamingOp("Reset", "busy")
  try {
    commandId = await workspaceReset(id, isDebug())
  } catch (err) {
    operationRunning = false
    setWorkspaceStatus(previousStatus)
    toasts.error(`Failed to reset: ${extractErrorMessage(err)}`)
  }
}

async function handleDelete() {
  confirmDeleteOpen = false
  const previousStatus = workspace?.status
  startStreamingOp("Delete", "deleting")
  deleting = true
  try {
    commandId = await workspaceDelete(id, isDebug())
  } catch (err) {
    operationRunning = false
    deleting = false
    setWorkspaceStatus(previousStatus)
    toasts.error(`Failed to delete: ${extractErrorMessage(err)}`)
  }
}

function startRename() {
  renameValue = id
  renaming = true
}

async function handleRename() {
  const trimmed = renameValue.trim()
  if (!trimmed || trimmed === id) {
    renaming = false
    return
  }
  if (hasContainer) {
    pendingRenameTarget = trimmed
    confirmRenameOpen = true
    return
  }
  await performRename(trimmed)
}

async function performRename(target: string) {
  renameSaving = true
  try {
    await workspaceRename(id, target)
    toasts.success(`Renamed workspace to ${target}`)
    renaming = false
    goto(`/workspaces/${target}`)
  } catch (err) {
    toasts.error(`Failed to rename: ${extractErrorMessage(err)}`)
  } finally {
    renameSaving = false
  }
}

async function handleRenameConfirmed() {
  await performRename(pendingRenameTarget)
  confirmRenameOpen = false
}
</script>

<div class="flex min-h-0 flex-1 flex-col gap-4">
  <Button variant="ghost" size="sm" class="w-fit" onclick={() => goto("/workspaces")}>
    &larr; Back
  </Button>

  {#if !workspace}
    <p class="text-muted-foreground">Workspace not found.</p>
  {:else}
    <!-- Hero banner -->
    <div class="rounded-xl border bg-card p-5">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div class="min-w-0 space-y-2">
          <div class="flex flex-wrap items-center gap-3">
            {#if renaming}
              <form class="flex items-center gap-2" onsubmit={(e) => { e.preventDefault(); handleRename() }}>
                <Input
                  data-slot="workspace-rename-input"
                  value={renameValue}
                  oninput={(e) => (renameValue = e.currentTarget.value)}
                  class="h-8 w-56 text-lg font-bold"
                  disabled={renameSaving}
                />
                <Button data-slot="workspace-rename-save" variant="outline" size="sm" type="submit" disabled={renameSaving || !renameValue.trim()}>
                  {renameSaving ? "Saving..." : "Save"}
                </Button>
                <Button data-slot="workspace-rename-cancel" variant="ghost" size="sm" type="button" onclick={() => (renaming = false)} disabled={renameSaving}>
                  Cancel
                </Button>
              </form>
            {:else}
              <h1 class="truncate text-2xl font-bold">{id}</h1>
              <Button data-slot="workspace-rename-btn" variant="ghost" size="icon-sm" onclick={startRename} disabled={operationRunning}>
                <Pencil class="h-4 w-4" />
                <span class="sr-only">Rename</span>
              </Button>
            {/if}
            <span class={badgeVariants({ variant: statusBadgeVariant() })}>
              {workspace.status ?? "Checking..."}
            </span>
            {#if operationRunning || isBusy}
              <Spinner class="size-3" />
            {/if}
            {#if inRecovery}
              <span class="inline-flex items-center gap-1 rounded-md border border-amber-500/40 bg-amber-500/10 px-2 py-0.5 text-xs font-medium text-amber-600 dark:text-amber-400">
                <LifeBuoy class="h-3 w-3" />
                Recovery mode
              </span>
            {/if}
          </div>

          <div class="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted-foreground">
            <span>{workspace.provider?.name ?? "No provider"}</span>
            {#if workspace.machine?.id}<span aria-hidden="true">&middot;</span><span>{workspace.machine.id}</span>{/if}
            {#if workspace.context}<span aria-hidden="true">&middot;</span><span>{workspace.context}</span>{/if}
          </div>

          <div class="flex items-center gap-1.5">
            <code class="rounded bg-muted px-2 py-1 font-mono text-xs">ssh {sshHost}</code>
            <Tooltip.Root>
              <Tooltip.Trigger>
                {#snippet child({ props })}
                  <Button variant="ghost" size="icon-sm" {...props} onclick={() => copyToClipboard(`ssh ${sshHost}`)}>
                    <ClipboardCopy class="h-3.5 w-3.5" />
                    <span class="sr-only">Copy SSH command</span>
                  </Button>
                {/snippet}
              </Tooltip.Trigger>
              <Tooltip.Content>Copy SSH command</Tooltip.Content>
            </Tooltip.Root>
          </div>
        </div>

        <ButtonGroup.Root class="shrink-0">
          <Button
            variant="destructive"
            size="sm"
            onclick={handleStop}
            disabled={operationRunning || (!isRunning && !isBusy)}
          >
            {#if operationRunning && operationLabel === "Stop"}<Spinner />{:else}<Square class="h-4 w-4" />{/if}
            Stop
          </Button>

          <Button
            variant="default"
            size="sm"
            onclick={handleStart}
            disabled={operationRunning || connecting || isRunning || isBusy || !isStopped}
          >
            {#if operationRunning && operationLabel === "Start"}<Spinner />{:else}<Play class="h-4 w-4" />{/if}
            Start
          </Button>

          <Button variant="outline" size="sm" onclick={handleOpenIde} disabled={!isRunning || operationRunning || currentIde === "none"}>
            {#if operationRunning && operationLabel === "Open IDE"}<Spinner />{:else}<Monitor class="h-4 w-4" />{/if}
            Open IDE
          </Button>
          {#if sshSessionId && !sshExited}
            <Button variant="secondary" size="sm" onclick={handleDisconnect}>
              <SquareTerminal class="h-4 w-4" />
              Disconnect
            </Button>
          {:else}
            <Button variant="outline" size="sm" onclick={async () => { if (sshSessionId) await handleDisconnect(); handleConnect() }} disabled={!isRunning || connecting}>
              {#if connecting}<Spinner />{:else}<SquareTerminal class="h-4 w-4" />{/if}
              SSH Terminal
            </Button>
          {/if}

          <DropdownMenu.Root>
            <DropdownMenu.Trigger>
              {#snippet child({ props })}
                <Button {...props} variant="outline" size="icon-sm">
                  <Ellipsis class="h-4 w-4" />
                  <span class="sr-only">More actions</span>
                </Button>
              {/snippet}
            </DropdownMenu.Trigger>
            <DropdownMenu.Content align="end">
              <DropdownMenu.Item onclick={() => (confirmRebuildOpen = true)} disabled={operationRunning}>
                <RotateCcw class="mr-2 h-4 w-4" />
                Rebuild
              </DropdownMenu.Item>
              <DropdownMenu.Item onclick={() => (confirmResetOpen = true)} disabled={operationRunning}>
                <RefreshCw class="mr-2 h-4 w-4" />
                Reset
              </DropdownMenu.Item>
              <DropdownMenu.Separator />
              <DropdownMenu.Item
                class="text-destructive data-[highlighted]:text-destructive"
                onclick={() => (confirmDeleteOpen = true)}
                disabled={operationRunning}
              >
                <Trash2 class="mr-2 h-4 w-4" />
                Delete
              </DropdownMenu.Item>
            </DropdownMenu.Content>
          </DropdownMenu.Root>
        </ButtonGroup.Root>
      </div>
    </div>

    {#if buildFailed}
      <div class="flex flex-col gap-3 rounded-lg border border-destructive/30 bg-destructive/10 p-4 sm:flex-row sm:items-center sm:justify-between">
        <div class="flex items-start gap-2 text-sm">
          <LifeBuoy class="mt-0.5 h-4 w-4 shrink-0 text-destructive" />
          <div>
            <p class="font-medium text-destructive">Dev container build failed</p>
            <p class="text-muted-foreground">Open a recovery container (features and lifecycle commands disabled) to repair devcontainer.json, or retry the build.</p>
          </div>
        </div>
        <ButtonGroup.Root class="shrink-0">
          {#if operationLabel !== "Recovery"}
            <Button variant="default" size="sm" onclick={handleRecovery} disabled={operationRunning}>
              <LifeBuoy class="h-4 w-4" />
              Reopen in Recovery Container
            </Button>
          {/if}
          <Button variant="outline" size="sm" onclick={handleStart} disabled={operationRunning}>
            <RefreshCw class="h-4 w-4" />
            Retry
          </Button>
          <Button variant="outline" size="sm" onclick={() => (activeTab = "logs")}>
            <ScrollText class="h-4 w-4" />
            Show Log
          </Button>
        </ButtonGroup.Root>
      </div>
    {:else if inRecovery}
      <div class="flex flex-col gap-3 rounded-lg border border-amber-500/40 bg-amber-500/10 p-4 sm:flex-row sm:items-center sm:justify-between">
        <div class="flex items-start gap-2 text-sm">
          <LifeBuoy class="mt-0.5 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400" />
          <div>
            <p class="font-medium text-amber-600 dark:text-amber-400">Running in recovery mode</p>
            <p class="text-muted-foreground">Features and lifecycle commands are disabled. Fix devcontainer.json, then rebuild the full container.</p>
          </div>
        </div>
        <Button variant="default" size="sm" onclick={handleRebuild} disabled={operationRunning}>
          {#if operationRunning && operationLabel === "Rebuild"}<Spinner />{:else}<RotateCcw class="h-4 w-4" />{/if}
          Rebuild full container
        </Button>
      </div>
    {/if}

    <Tabs.Root bind:value={activeTab} class="min-h-0 flex-1 overflow-hidden">
      <Tabs.List class="h-9 w-fit">
        <Tabs.Trigger value="overview" class={tabTriggerClass}>Overview</Tabs.Trigger>
        <Tabs.Trigger value="logs" class={tabTriggerClass}>Logs</Tabs.Trigger>
        <Tabs.Trigger value="terminal" class={tabTriggerClass}>Terminal</Tabs.Trigger>
      </Tabs.List>

      <Tabs.Content value="overview">
        <div class="mt-4 grid grid-cols-2 gap-4 text-sm">
          <div class="text-muted-foreground">ID</div>
          <div>{workspace.id}</div>

          <div class="text-muted-foreground">UID</div>
          <div>{workspace.uid ?? "N/A"}</div>

          <div class="text-muted-foreground">IDE</div>
          <div>
            <Popover.Root bind:open={ideComboOpen}>
              <Popover.Trigger>
                {#snippet child({ props })}
                  <Button variant="outline" class="h-8 w-48 justify-between text-left" {...props}>
                    <span class="truncate">{IDE_OPTIONS.find((i) => i.value === currentIde)?.label ?? "None"}</span>
                    <ChevronsUpDown class="ml-2 h-4 w-4 shrink-0 opacity-50" />
                  </Button>
                {/snippet}
              </Popover.Trigger>
              <Popover.Content class="w-48 p-0" align="start">
                <Command.Root shouldFilter={false}>
                  <Command.Input placeholder="Search IDEs..." bind:value={ideSearch} />
                  <Command.List class="max-h-60">
                    <Command.Empty>No IDE found.</Command.Empty>
                    <Command.Group>
                      {#each filteredIdes as ide (ide.value)}
                        <Command.Item
                          value={ide.value}
                          class="justify-start"
                          onSelect={async () => {
                            const seq = ++ideSetSeq
                            const prev = selectedIde
                            selectedIde = ide.value
                            ideComboOpen = false
                            ideSearch = ""
                            try {
                              await workspaceSetIde(id, ide.value)
                            } catch (err) {
                              if (seq !== ideSetSeq) return
                              selectedIde = prev
                              toasts.error(`Failed to set IDE: ${extractErrorMessage(err)}`)
                            }
                          }}
                        >
                          <Check class="mr-2 h-4 w-4 {currentIde === ide.value ? 'opacity-100' : 'opacity-0'}" />
                          {ide.label}
                        </Command.Item>
                      {/each}
                    </Command.Group>
                  </Command.List>
                </Command.Root>
              </Popover.Content>
            </Popover.Root>
          </div>

          <div class="text-muted-foreground">Workspace Folder</div>
          <div>
            {#if editingFolder}
              <form class="flex items-center gap-2" onsubmit={(e) => { e.preventDefault(); setWorkspaceFolder(id, folderValue.trim()); customFolder = folderValue.trim(); editingFolder = false; toasts.success("Workspace folder updated") }}>
                <Input
                  value={folderValue}
                  oninput={(e) => (folderValue = e.currentTarget.value)}
                  placeholder="/workspaces/my-project"
                  class="h-7 w-56 text-xs font-mono"
                />
                <Button variant="outline" size="sm" type="submit" class="h-7 text-xs">Save</Button>
                <Button variant="ghost" size="sm" type="button" class="h-7 text-xs" onclick={() => (editingFolder = false)}>Cancel</Button>
              </form>
            {:else}
              <span class="inline-flex items-center gap-2">
                <span class="font-mono text-xs">{customFolder || "Default"}</span>
                <Button variant="ghost" size="icon-sm" onclick={() => { folderValue = customFolder; editingFolder = true }} disabled={operationRunning}>
                  <Pencil class="h-3 w-3" />
                  <span class="sr-only">Edit workspace folder</span>
                </Button>
              </span>
            {/if}
          </div>

          <div class="text-muted-foreground">Source</div>
          <div class="truncate">
            {workspace.source?.gitRepository
              ?? workspace.source?.localFolder
              ?? workspace.source?.image
              ?? "N/A"}
          </div>

          {#if workspace.source?.gitBranch}
            <div class="text-muted-foreground">Branch</div>
            <div>{workspace.source.gitBranch}</div>
          {/if}

          <div class="text-muted-foreground">Created</div>
          <div>{workspace.creationTimestamp ? formatTimestamp(workspace.creationTimestamp) : "N/A"}</div>

          <div class="text-muted-foreground">Last Used</div>
          <div>{workspace.lastUsed ? formatTimestamp(workspace.lastUsed) : "N/A"}</div>
        </div>
      </Tabs.Content>

      <Tabs.Content value="logs" class="min-h-0 flex-1 overflow-hidden">
        <div class="mt-4 flex min-h-0 flex-1 flex-col h-full overflow-hidden">
          <Accordion.Root type="multiple" value={["live-output"]} class="w-full overflow-hidden">
            <Accordion.Item value="live-output">
              <Accordion.Trigger>
                Live Output
                {#if outputLines.length > 0}
                  <span class="ml-2 text-xs text-muted-foreground">({outputLines.length} lines)</span>
                {/if}
                {#if operationRunning}
                  <span class="ml-2 text-xs text-muted-foreground animate-pulse">streaming...</span>
                {/if}
              </Accordion.Trigger>
              <Accordion.Content>
                {#if outputLines.length > 0}
                  <div class="flex justify-end mb-2">
                    <Tooltip.Root>
                      <Tooltip.Trigger>
                        {#snippet child({ props })}
                          <Button variant="ghost" size="icon-sm" {...props} onclick={() => copyToClipboard(outputLines.map(stripAnsi).join("\n"))}>
                            <ClipboardCopy class="h-4 w-4" />
                          </Button>
                        {/snippet}
                      </Tooltip.Trigger>
                      <Tooltip.Content>Copy output</Tooltip.Content>
                    </Tooltip.Root>
                  </div>
                {/if}
                {#if outputLines.length === 0}
                  <div class="flex items-center justify-center rounded-md border p-4">
                    <p class="text-sm text-muted-foreground">
                      {operationRunning ? "Waiting for output..." : "No output yet. Run an operation to see live output."}
                    </p>
                  </div>
                {:else}
                  <LogTable lines={outputLines} follow />
                {/if}
              </Accordion.Content>
            </Accordion.Item>

            <Accordion.Item value="log-files">
              <Accordion.Trigger>
                Log Files
                {#if logEntries.length > 0}
                  <span class="ml-2 text-xs text-muted-foreground">({logEntries.length} files)</span>
                {/if}
              </Accordion.Trigger>
              <Accordion.Content>
                {#if logsLoading}
                  <div class="rounded-md border">
                    <div class="space-y-3 p-4">
                      {#each { length: 4 } as _}
                        <Skeleton class="h-4 w-full" />
                      {/each}
                    </div>
                  </div>
                {:else if logEntries.length === 0}
                  <p class="text-sm text-muted-foreground">No log files found for this workspace.</p>
                {:else}
                  <Accordion.Root type="single" class="w-full">
                    {#each logEntries as entry (entry.filename)}
                      <Accordion.Item value={entry.filename}>
                        <div class="group/log flex items-center">
                          <Accordion.Trigger class="flex-1" onclick={() => viewLog(entry)}>
                            <span class="truncate">{entry.filename}</span>
                            <span class="ml-2 text-xs text-muted-foreground">{Math.round(entry.sizeBytes / 1024)}KB</span>
                          </Accordion.Trigger>
                          <div class="flex items-center gap-1 shrink-0 pr-2">
                            {#if selectedLog === entry.filename && logContent}
                              <Tooltip.Root>
                                <Tooltip.Trigger>
                                  {#snippet child({ props })}
                                    <button
                                      type="button"
                                      class="rounded p-1.5 opacity-0 transition-opacity hover:bg-muted group-hover/log:opacity-60 hover:!opacity-100"
                                      {...props}
                                      onclick={() => copyToClipboard(logContent)}
                                    >
                                      <ClipboardCopy class="h-3.5 w-3.5" />
                                    </button>
                                  {/snippet}
                                </Tooltip.Trigger>
                                <Tooltip.Content>Copy log</Tooltip.Content>
                              </Tooltip.Root>
                            {/if}
                            <Tooltip.Root>
                              <Tooltip.Trigger>
                                {#snippet child({ props })}
                                  <button
                                    type="button"
                                    class="rounded p-1.5 opacity-0 transition-opacity hover:bg-destructive/10 hover:text-destructive group-hover/log:opacity-60 hover:!opacity-100"
                                    {...props}
                                    onclick={() => deleteLog(entry)}
                                  >
                                    <Trash2 class="h-3.5 w-3.5" />
                                  </button>
                                {/snippet}
                              </Tooltip.Trigger>
                              <Tooltip.Content>Delete log</Tooltip.Content>
                            </Tooltip.Root>
                          </div>
                        </div>
                        <Accordion.Content>
                          {#if selectedLog === entry.filename}
                            <LogTable lines={logContent.split("\n")} />
                          {:else}
                            <p class="rounded-md border p-4 text-sm text-muted-foreground">Loading...</p>
                          {/if}
                        </Accordion.Content>
                      </Accordion.Item>
                    {/each}
                  </Accordion.Root>
                {/if}
              </Accordion.Content>
            </Accordion.Item>
          </Accordion.Root>
        </div>
      </Tabs.Content>

      <Tabs.Content value="terminal" class="relative min-h-0 flex-1 overflow-hidden">
        <div class="absolute inset-0 mt-4 flex flex-col">
          {#if sshSessionId}
            <div class="min-h-0 flex-1 rounded-md border overflow-hidden">
              {#if sshConnectionFailed}
                <div class="flex h-full items-center justify-center bg-muted/50">
                  <div class="text-center">
                    <SquareTerminal class="mx-auto h-8 w-8 text-muted-foreground/50" />
                    <p class="mt-2 text-sm font-medium">SSH connection failed</p>
                    <p class="mt-1 text-xs text-muted-foreground">The workspace may not be running or the SSH server is not available.</p>
                  </div>
                </div>
              {:else}
                <TerminalComponent
                  sessionId={sshSessionId}
                  onExit={handleSshExit}
                  onGpgForwardFailed={handleGpgForwardFailed}
                />
              {/if}
            </div>
            {#if sshExited}
              <div class="mt-2 flex items-center justify-end gap-2 shrink-0">
                <span class="text-sm text-muted-foreground">{sshConnectionFailed ? "Connection failed" : "Session ended"}</span>
                <Button variant="outline" size="sm" onclick={handleDisconnect}>Close</Button>
                <Button size="sm" onclick={async () => { await handleDisconnect(); handleConnect() }} disabled={connecting}>
                  {connecting ? "Reconnecting..." : "Reconnect"}
                </Button>
              </div>
            {/if}
          {:else}
            <div class="flex min-h-0 flex-1 items-center justify-center rounded-md border bg-muted/50">
              <div class="text-center">
                <p class="text-muted-foreground">No active terminal session.</p>
                <Button size="sm" class="mt-3" onclick={handleConnect} disabled={connecting || isStopped}>
                  {connecting ? "Connecting..." : "Connect to workspace"}
                </Button>
              </div>
            </div>
          {/if}
        </div>
      </Tabs.Content>

    </Tabs.Root>
  {/if}
</div>

<ConfirmDialog
  bind:open={confirmRebuildOpen}
  title="Rebuild workspace"
  description="This recreates the container for '{id}' from its devcontainer config. Your source code is kept, but anything installed inside the container outside the devcontainer config (e.g. manual 'apt install', files outside bind mounts) will be lost. Use this to pick up devcontainer changes or recover from a broken container."
  confirmLabel="Rebuild"
  variant="default"
  onconfirm={handleRebuild}
/>

<ConfirmDialog
  bind:open={confirmResetOpen}
  title="Reset workspace"
  description="This removes the container for '{id}' along with its cloned source code, then recreates everything from scratch. Any uncommitted changes and files not pushed to your repository will be permanently lost. This action cannot be undone."
  confirmLabel="Reset"
  onconfirm={handleReset}
/>

<ConfirmDialog
  bind:open={confirmDeleteOpen}
  title="Delete workspace"
  description="This will permanently delete workspace '{id}' and all associated data. This action cannot be undone."
  confirmLabel="Delete"
  loading={deleting}
  onconfirm={handleDelete}
/>

<ConfirmDialog
  bind:open={confirmRenameOpen}
  title="Rename will reset the container"
  description="Renaming '{id}' to '{pendingRenameTarget}' will delete the existing container. The source code and devcontainer config are preserved, but anything installed inside the running container outside the devcontainer config (e.g. manual 'apt install', files outside bind mounts) will be lost."
  confirmLabel="Rename"
  loading={renameSaving}
  onconfirm={handleRenameConfirmed}
/>
