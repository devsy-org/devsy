<script lang="ts">
import { page } from "$app/stores"
import { goto } from "$app/navigation"
import { onMount, onDestroy } from "svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import { Separator } from "$lib/components/ui/separator/index.js"
import * as Tabs from "$lib/components/ui/tabs/index.js"
import { ScrollArea } from "$lib/components/ui/scroll-area/index.js"
import { workspaces } from "$lib/stores/workspaces.js"
import {
  workspaceStop,
  workspaceRebuild,
  workspaceDelete,
  workspaceLogsList,
  workspaceLogRead,
} from "$lib/ipc/commands.js"
import { onCommandProgress } from "$lib/ipc/events.js"
import type { LogEntry } from "$lib/types/index.js"
import type { UnlistenFn } from "@tauri-apps/api/event"

let id = $derived($page.params.id)
let workspace = $derived($workspaces.find((ws) => ws.id === id))

let outputLines = $state<string[]>([])
let unlisten: UnlistenFn | null = null

let logEntries = $state<LogEntry[]>([])
let selectedLog = $state<string | null>(null)
let logContent = $state<string>("")
let logsLoading = $state(false)

onMount(async () => {
  try {
    unlisten = await onCommandProgress((progress) => {
      if (progress.message) {
        outputLines = [...outputLines, progress.message]
      }
    })
  } catch {
    // Event listener setup failed
  }

  loadLogs()
})

onDestroy(() => {
  unlisten?.()
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

async function handleStop() {
  try {
    await workspaceStop(id)
  } catch {
    // handled by event system
  }
}

async function handleRebuild() {
  try {
    await workspaceRebuild(id)
  } catch {
    // handled by event system
  }
}

async function handleDelete() {
  try {
    await workspaceDelete(id)
    goto("/workspaces")
  } catch {
    // handled by event system
  }
}
</script>

<div class="space-y-6">
  <div class="flex items-center gap-4">
    <Button variant="ghost" size="sm" onclick={() => goto("/workspaces")}>
      &larr; Back
    </Button>
    <h1 class="text-2xl font-bold">{id}</h1>
    {#if workspace?.provider?.name}
      <span class={badgeVariants({ variant: "secondary" })}>{workspace.provider.name}</span>
    {/if}
    {#if workspace?.status}
      <span class={badgeVariants({ variant: "default" })}>{workspace.status}</span>
    {/if}
  </div>

  <div class="flex gap-2">
    <Button variant="outline" size="sm" onclick={handleStop}>Stop</Button>
    <Button variant="outline" size="sm" onclick={handleRebuild}>Rebuild</Button>
    <Button variant="destructive" size="sm" onclick={handleDelete}>Delete</Button>
  </div>

  <Separator />

  {#if !workspace}
    <p class="text-muted-foreground">Workspace not found.</p>
  {:else}
    <Tabs.Root value="overview">
      <Tabs.List>
        <Tabs.Trigger value="overview">Overview</Tabs.Trigger>
        <Tabs.Trigger value="output">Live Output</Tabs.Trigger>
        <Tabs.Trigger value="logs">Logs</Tabs.Trigger>
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
          <div>{workspace.ide?.name ?? "N/A"}</div>

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
          <div>{workspace.creationTimestamp ?? "N/A"}</div>

          <div class="text-muted-foreground">Last Used</div>
          <div>{workspace.lastUsedTimestamp ?? "N/A"}</div>

          <div class="text-muted-foreground">Context</div>
          <div>{workspace.context ?? "N/A"}</div>
        </div>
      </Tabs.Content>

      <Tabs.Content value="output">
        <ScrollArea class="mt-4 h-96 rounded-md border bg-muted/50 p-4">
          {#if outputLines.length === 0}
            <p class="text-sm text-muted-foreground">
              No output yet. Run a command to see live output.
            </p>
          {:else}
            <pre class="text-xs font-mono whitespace-pre-wrap">{outputLines.join("\n")}</pre>
          {/if}
        </ScrollArea>
      </Tabs.Content>

      <Tabs.Content value="logs">
        <div class="mt-4 space-y-4">
          {#if logsLoading}
            <p class="text-sm text-muted-foreground">Loading logs...</p>
          {:else if logEntries.length === 0}
            <p class="text-sm text-muted-foreground">
              No logs found for this workspace.
            </p>
          {:else}
            <div class="flex gap-4">
              <div class="w-64 space-y-1">
                {#each logEntries as entry}
                  <button
                    class="w-full rounded px-3 py-2 text-left text-sm hover:bg-muted {selectedLog === entry.filename ? 'bg-muted font-medium' : ''}"
                    onclick={() => viewLog(entry)}
                  >
                    <div class="truncate">{entry.filename}</div>
                    <div class="text-xs text-muted-foreground">
                      {Math.round(entry.sizeBytes / 1024)}KB
                    </div>
                  </button>
                {/each}
              </div>
              <ScrollArea class="h-96 flex-1 rounded-md border bg-muted/50 p-4">
                {#if selectedLog}
                  <pre class="text-xs font-mono whitespace-pre-wrap">{logContent}</pre>
                {:else}
                  <p class="text-sm text-muted-foreground">Select a log file to view.</p>
                {/if}
              </ScrollArea>
            </div>
          {/if}
        </div>
      </Tabs.Content>
    </Tabs.Root>
  {/if}
</div>
