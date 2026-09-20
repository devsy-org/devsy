<script lang="ts">
import WorkspaceOperation from "./WorkspaceOperation.svelte"
import { workspaceJobs } from "$lib/stores/workspaces.js"
import { workspaceJobBusy, workspaceJobInterruptible } from "$shared/workspace-operation.js"
import { goto } from "$lib/router.js"
import { Button } from "$lib/components/ui/button/index.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import ConfirmDialog from "$lib/components/layout/ConfirmDialog.svelte"
import { workspaceStop, workspaceDelete } from "$lib/ipc/commands.js"
import { toasts } from "$lib/stores/toasts.js"
import { extractErrorMessage } from "$lib/utils/error.js"
import type { Workspace } from "$lib/types/index.js"
import { timeAgo } from "$lib/utils/time.js"

let { workspace }: { workspace: Workspace } = $props()
let confirmDeleteOpen = $state(false)
let deleting = $state(false)
let acting = $state(false)
let busy = $derived(acting || workspaceJobBusy($workspaceJobs[workspace.id]))

let isRunning = $derived(workspace.status?.toLowerCase() === "running")
let isStopped = $derived(
  !workspace.status ||
    workspace.status.toLowerCase() === "stopped" ||
    workspace.status.toLowerCase() === "notfound",
)
let isBusy = $derived(workspace.status?.toLowerCase() === "busy")

function sourceDisplay(ws: Workspace): string {
  if (ws.source?.gitRepository) return ws.source.gitRepository
  if (ws.source?.localFolder) return ws.source.localFolder
  if (ws.source?.image) return ws.source.image
  return "Unknown source"
}

function handleOpen(e: Event) {
  e.stopPropagation()
  goto(`/workspaces/${workspace.id}?action=open-ide`)
}

function handleStart(e: Event) {
  e.stopPropagation()
  goto(`/workspaces/${workspace.id}?action=start`)
}

async function handleStop(e: Event) {
  e.stopPropagation()
  acting = true
  try {
    await workspaceStop(workspace.id)
  } catch (err) {
    toasts.error(`Failed to stop: ${extractErrorMessage(err)}`)
  } finally {
    acting = false
  }
}

function openDeleteConfirm(e: Event) {
  e.stopPropagation()
  confirmDeleteOpen = true
}

async function handleDelete() {
  deleting = true
  try {
    await workspaceDelete(workspace.id)
    confirmDeleteOpen = false
  } catch (err) {
    toasts.error(`Failed to delete: ${extractErrorMessage(err)}`)
  } finally {
    deleting = false
  }
}
</script>

<button
  type="button"
  class="rounded-xl border bg-card p-6 text-left text-card-foreground shadow-sm transition-colors hover:bg-accent/50 w-full"
  onclick={() => goto(`/workspaces/${workspace.id}`)}
>
  <div class="flex items-start justify-between gap-3">
    <h3 class="text-lg font-semibold truncate">{workspace.id}</h3>
    <span class="text-xs text-muted-foreground whitespace-nowrap pt-1">
      {timeAgo(workspace.lastUsed)}
    </span>
  </div>

  <p class="mt-2 text-sm text-muted-foreground truncate">
    {sourceDisplay(workspace)}
  </p>

  <div class="mt-4 flex flex-wrap items-center gap-2">
    {#if workspace.provider?.name}
      <span class={badgeVariants({ variant: "secondary" })}>
        {workspace.provider.name}
      </span>
    {/if}
    {#if workspace.ide?.name}
      <span class={badgeVariants({ variant: "outline" })}>
        {workspace.ide.name}
      </span>
    {/if}
    <WorkspaceOperation id={workspace.id} status={workspace.status} />
  </div>

  <div class="mt-4 flex items-center gap-2">
    {#if isRunning}
      <Button size="sm" disabled={busy} onclick={handleOpen}>
        Open
      </Button>
    {:else if isStopped}
      <Button size="sm" disabled={busy} onclick={handleStart}>Start</Button>
    {/if}
    {#if isRunning || isBusy || workspaceJobInterruptible($workspaceJobs[workspace.id])}
      <Button variant="outline" size="sm" onclick={handleStop} disabled={busy && !workspaceJobInterruptible($workspaceJobs[workspace.id])}>
        {acting ? "Stopping..." : "Stop"}
      </Button>
    {/if}
    <Button variant="outline" size="sm" onclick={(e) => { e.stopPropagation(); goto(`/workspaces/${workspace.id}`) }}>
      Details
    </Button>
    <Button variant="destructive" size="sm" onclick={openDeleteConfirm} disabled={busy && !workspaceJobInterruptible($workspaceJobs[workspace.id])}>Delete</Button>
  </div>
</button>

<ConfirmDialog
  bind:open={confirmDeleteOpen}
  title="Delete workspace"
  description="This will permanently delete workspace '{workspace.id}' and all associated data. This action cannot be undone."
  confirmLabel="Delete"
  loading={deleting}
  onconfirm={handleDelete}
/>
