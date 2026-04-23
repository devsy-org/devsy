<script lang="ts">
import { goto, querystring } from "$lib/router.js"
import { onMount, onDestroy } from "svelte"
import { Check, ChevronsUpDown, ClipboardCopy, Ellipsis, Monitor, Play, RefreshCw, RotateCcw, Square, SquareTerminal, Trash2 } from "@lucide/svelte"
import * as Tooltip from "$lib/components/ui/tooltip/index.js"
import { Spinner } from "$lib/components/ui/spinner/index.js"
import { Button } from "$lib/components/ui/button/index.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import * as Command from "$lib/components/ui/command/index.js"
import * as Popover from "$lib/components/ui/popover/index.js"
import * as DropdownMenu from "$lib/components/ui/dropdown-menu/index.js"
import { Separator } from "$lib/components/ui/separator/index.js"
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
  workspaceLogsList,
  workspaceLogRead,
  workspaceLogDelete,
  auditByResource,
} from "$lib/ipc/commands.js"
import { onCommandProgress } from "$lib/ipc/events.js"
import { toasts } from "$lib/stores/toasts.js"
import { extractErrorMessage } from "$lib/utils/error.js"
import type { AuditEntry, LogEntry } from "$lib/types/index.js"
import type { UnlistenFn } from "$lib/ipc/types.js"
import { formatTimestamp } from "$lib/utils/time.js"
import { stripAnsi } from "$lib/utils/log-parser.js"

let { params = {} }: { params?: Record<string, string> } = $props()

const IDE_OPTIONS = [
  { value: "none", label: "None" },
  { value: "vscode", label: "VS Code" },
  { value: "openvscode", label: "VS Code Browser" },
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
  { value: "vscode-insiders", label: "VS Code Insiders" },
  { value: "positron", label: "Positron" },
  { value: "rstudio", label: "RStudio Server" },
]

let id = $derived(params.id ?? "")
let workspace = $derived($workspaces.find((ws) => ws.id === id))

let isRunning = $derived(workspace?.status?.toLowerCase() === "running")
let isStopped = $derived(
  !workspace?.status ||
    workspace.status.toLowerCase() === "stopped" ||
    workspace.status.toLowerCase() === "notfound",
)
let isBusy = $derived(workspace?.status?.toLowerCase() === "busy")

function statusBadgeVariant(): "default" | "secondary" | "outline" {
  if (isRunning) return "default"
  if (isBusy) return "secondary"
  return "outline"
}

let activeTab = $state("overview")
let outputLines = $state<string[]>([])
let commandId = $state<string | null>(null)
let operationLabel = $state("")
let operationRunning = $state(false)
let unlisten: UnlistenFn | null = null
let tableEndEl = $state<HTMLDivElement | null>(null)

let logEntries = $state<LogEntry[]>([])
let selectedLog = $state<string | null>(null)
let logContent = $state<string>("")
let logsLoading = $state(false)

let auditEntries = $state<AuditEntry[]>([])
let auditLoading = $state(false)
let confirmDeleteOpen = $state(false)
let deleting = $state(false)

let sshSessionId = $state<string | null>(null)
let sshExited = $state(false)
let connecting = $state(false)
let ideComboOpen = $state(false)
let ideSearch = $state("")
let selectedIde = $state<string | null>(null)
let currentIde = $derived(selectedIde ?? workspace?.ide?.name ?? "none")
let filteredIdes = $derived(
  IDE_OPTIONS.filter((i) =>
    i.label.toLowerCase().includes(ideSearch.toLowerCase()),
  ),
)

function scrollToBottom() {
  if (tableEndEl) {
    tableEndEl.scrollIntoView({ block: "end", behavior: "smooth" })
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
        if (progress.message) {
          outputLines = [...outputLines, progress.message]
          requestAnimationFrame(scrollToBottom)
        }
        if (progress.done) {
          operationRunning = false
          const success = stripAnsi(progress.message).includes("Exit code: 0")
          if (success) {
            toasts.success(`${operationLabel} ${id} succeeded`)
          } else {
            toasts.error(
              `${operationLabel} ${id} failed. Check output for details.`,
            )
          }
          if (operationLabel === "Delete" && success) {
            goto("/workspaces")
            return
          }
          loadAudit()
          loadLogs()
        }
      }
    })
  } catch {
    // Event listener setup failed
  }

  loadLogs()
  loadAudit()

  // Auto-trigger IDE open when navigated with ?action=open-ide
  const qs = new URLSearchParams($querystring ?? "")
  const action = qs.get("action")
  if (action === "open-ide") {
    // Clear query param so refresh doesn't re-trigger
    history.replaceState({}, "", window.location.pathname + window.location.hash.split("?")[0])
    handleOpenIde()
  }
})

onDestroy(() => {
  unlisten?.()
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

async function loadAudit() {
  auditLoading = true
  try {
    auditEntries = await auditByResource("workspace", id)
  } catch {
    auditEntries = []
  } finally {
    auditLoading = false
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
  sshExited = false
  try {
    const sessionId = await terminalCreateSsh(id, 80, 24)
    sshSessionId = sessionId
    addTerminal({
      id: sessionId,
      label: `SSH: ${id}`,
      type: "ssh",
      workspaceId: id,
    })
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
}

function handleSshExit(_exitCode?: number, _signal?: number) {
  if (sshSessionId) {
    sshExited = true
  }
}

function startStreamingOp(label: string) {
  operationLabel = label
  operationRunning = true
  outputLines = []
  activeTab = "logs"
}

async function handleStart() {
  startStreamingOp("Start")
  try {
    commandId = await workspaceUp({ source: id })
  } catch (err) {
    operationRunning = false
    toasts.error(`Failed to start: ${extractErrorMessage(err)}`)
  }
}

async function handleOpenIde() {
  const ide = currentIde !== "none" ? currentIde : undefined
  startStreamingOp("Open IDE")
  try {
    commandId = await workspaceUp({ source: id, ide })
  } catch (err) {
    operationRunning = false
    toasts.error(`Failed to open IDE: ${extractErrorMessage(err)}`)
  }
}

async function handleStop() {
  startStreamingOp("Stop")
  try {
    commandId = await workspaceStop(id)
  } catch (err) {
    operationRunning = false
    toasts.error(`Failed to stop: ${extractErrorMessage(err)}`)
  }
}

async function handleRebuild() {
  startStreamingOp("Rebuild")
  try {
    commandId = await workspaceRebuild(id)
  } catch (err) {
    operationRunning = false
    toasts.error(`Failed to rebuild: ${extractErrorMessage(err)}`)
  }
}

async function handleReset() {
  startStreamingOp("Reset")
  try {
    commandId = await workspaceReset(id)
  } catch (err) {
    operationRunning = false
    toasts.error(`Failed to reset: ${extractErrorMessage(err)}`)
  }
}

async function handleDelete() {
  confirmDeleteOpen = false
  startStreamingOp("Delete")
  deleting = true
  try {
    commandId = await workspaceDelete(id)
  } catch (err) {
    operationRunning = false
    deleting = false
    toasts.error(`Failed to delete: ${extractErrorMessage(err)}`)
  }
}
</script>

<div class="flex min-h-0 flex-1 flex-col gap-6">
  <div class="flex items-center gap-4">
    <Button variant="ghost" size="sm" onclick={() => goto("/workspaces")}>
      &larr; Back
    </Button>
    <h1 class="text-2xl font-bold">{id}</h1>
    {#if workspace?.provider?.name}
      <span class={badgeVariants({ variant: "secondary" })}>{workspace.provider.name}</span>
    {/if}
    {#if workspace?.status}
      <span class={badgeVariants({ variant: statusBadgeVariant() })}>{workspace.status}</span>
    {/if}
  </div>

  {#if workspace}
    <div class="flex items-center gap-2">
      {#if isRunning || isBusy}
        <Button size="sm" onclick={handleStop} disabled={operationRunning}>
          {#if operationRunning && operationLabel === "Stop"}<Spinner />{:else}<Square class="h-4 w-4" />{/if}
          Stop
        </Button>
      {:else}
        <Button size="sm" onclick={handleStart} disabled={!isStopped || operationRunning || connecting}>
          {#if operationRunning && operationLabel === "Start"}<Spinner />{:else}<Play class="h-4 w-4" />{/if}
          Start
        </Button>
      {/if}

      {#if isRunning}
        <Button variant="outline" size="sm" onclick={handleOpenIde} disabled={operationRunning}>
          {#if operationRunning && operationLabel === "Open IDE"}<Spinner />{:else}<Monitor class="h-4 w-4" />{/if}
          Open IDE
        </Button>
        {#if sshSessionId && !sshExited}
          <Button variant="outline" size="sm" onclick={handleDisconnect}>
            <SquareTerminal class="h-4 w-4" />
            Disconnect
          </Button>
        {:else}
          <Button variant="outline" size="sm" onclick={async () => { if (sshSessionId) await handleDisconnect(); handleConnect() }} disabled={!isRunning || connecting}>
            {#if connecting}<Spinner />{:else}<SquareTerminal class="h-4 w-4" />{/if}
            SSH Terminal
          </Button>
        {/if}
      {/if}

      <DropdownMenu.Root>
        <DropdownMenu.Trigger>
          {#snippet child({ props })}
            <Button {...props} variant="outline" size="icon" class="h-8 w-8">
              <Ellipsis class="h-4 w-4" />
              <span class="sr-only">More actions</span>
            </Button>
          {/snippet}
        </DropdownMenu.Trigger>
        <DropdownMenu.Content align="end">
          <DropdownMenu.Item onclick={handleRebuild} disabled={operationRunning}>
            <RotateCcw class="mr-2 h-4 w-4" />
            Rebuild
          </DropdownMenu.Item>
          <DropdownMenu.Item onclick={handleReset} disabled={operationRunning}>
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
    </div>
  {/if}

  <Separator />

  {#if !workspace}
    <p class="text-muted-foreground">Workspace not found.</p>
  {:else}
    <Tabs.Root bind:value={activeTab} class="min-h-0 flex-1 overflow-hidden">
      <Tabs.List variant="line">
        <Tabs.Trigger value="overview">Overview</Tabs.Trigger>
        <Tabs.Trigger value="logs">Logs</Tabs.Trigger>
        <Tabs.Trigger value="terminal">Terminal</Tabs.Trigger>
        <Tabs.Trigger value="activity">Activity</Tabs.Trigger>
      </Tabs.List>

      <Tabs.Content value="overview">
        <div class="mt-4 grid grid-cols-2 gap-4 text-sm">
          <div class="text-muted-foreground">ID</div>
          <div>{workspace.id}</div>

          <div class="text-muted-foreground">UID</div>
          <div>{workspace.uid ?? "N/A"}</div>

          <div class="text-muted-foreground">Provider</div>
          <div>{workspace.provider?.name ?? "N/A"}</div>

          <div class="text-muted-foreground">Machine</div>
          <div>{workspace.machine?.id ?? "N/A"}</div>

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
                          onSelect={() => {
                            selectedIde = ide.value
                            ideComboOpen = false
                            ideSearch = ""
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

          <div class="text-muted-foreground">Status</div>
          <div>{workspace.status ?? "Unknown"}</div>

          <div class="text-muted-foreground">Created</div>
          <div>{workspace.created ? formatTimestamp(workspace.created) : "N/A"}</div>

          <div class="text-muted-foreground">Last Used</div>
          <div>{workspace.lastUsed ? formatTimestamp(workspace.lastUsed) : "N/A"}</div>

          <div class="text-muted-foreground">Context</div>
          <div>{workspace.context ?? "N/A"}</div>
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
                <div class="max-h-96 overflow-auto rounded-md border">
                  {#if outputLines.length === 0}
                    <div class="flex items-center justify-center p-4">
                      <p class="text-sm text-muted-foreground">
                        {operationRunning ? "Waiting for output..." : "No output yet. Run an operation to see live output."}
                      </p>
                    </div>
                  {:else}
                    <LogTable lines={outputLines} />
                    <div bind:this={tableEndEl}></div>
                  {/if}
                </div>
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
                  <p class="text-sm text-muted-foreground">Loading logs...</p>
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
                                      onclick={() => copyToClipboard(logContent)}
                                      {...props}
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
                                    onclick={() => deleteLog(entry)}
                                    {...props}
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
                          <div class="max-h-96 overflow-auto rounded-md border">
                            {#if selectedLog === entry.filename}
                              <LogTable lines={logContent.split("\n")} />
                            {:else}
                              <p class="p-4 text-sm text-muted-foreground">Loading...</p>
                            {/if}
                          </div>
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
              <TerminalComponent sessionId={sshSessionId} onExit={handleSshExit} />
            </div>
            <div class="mt-2 flex items-center justify-end gap-2 shrink-0">
              {#if sshExited}
                <span class="text-sm text-muted-foreground">Session ended</span>
                <Button variant="outline" size="sm" onclick={handleDisconnect}>Close</Button>
                <Button size="sm" onclick={async () => { await handleDisconnect(); handleConnect() }} disabled={connecting}>
                  {connecting ? "Reconnecting..." : "Reconnect"}
                </Button>
              {:else}
                <Button variant="outline" size="sm" onclick={handleDisconnect}>Disconnect</Button>
              {/if}
            </div>
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

      <Tabs.Content value="activity">
        <div class="mt-4 space-y-4">
          {#if auditLoading}
            <p class="text-sm text-muted-foreground">Loading activity...</p>
          {:else if auditEntries.length === 0}
            <p class="text-sm text-muted-foreground">
              No activity recorded for this workspace.
            </p>
          {:else}
            <div class="divide-y rounded-md border">
              {#each auditEntries as entry}
                <div class="flex items-center gap-3 px-4 py-3">
                  <span
                    class={badgeVariants({
                      variant: entry.success ? "default" : "destructive",
                    })}
                  >
                    {entry.action}
                  </span>
                  <div class="min-w-0 flex-1">
                    {#if entry.details}
                      <span class="text-sm text-muted-foreground">{entry.details}</span>
                    {/if}
                  </div>
                  <span class="shrink-0 text-xs text-muted-foreground">
                    {formatTimestamp(entry.timestamp)}
                  </span>
                </div>
              {/each}
            </div>
          {/if}
        </div>
      </Tabs.Content>
    </Tabs.Root>
  {/if}
</div>

<ConfirmDialog
  bind:open={confirmDeleteOpen}
  title="Delete workspace"
  description="This will permanently delete workspace '{id}' and all associated data. This action cannot be undone."
  confirmLabel="Delete"
  loading={deleting}
  onconfirm={handleDelete}
/>
