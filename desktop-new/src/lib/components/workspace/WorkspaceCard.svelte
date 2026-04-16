<script lang="ts">
import { goto } from "$app/navigation"
import { Button } from "$lib/components/ui/button/index.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import { workspaceStop, workspaceDelete } from "$lib/ipc/commands.js"
import { toasts } from "$lib/stores/toasts.js"
import type { Workspace } from "$lib/types/index.js"

let { workspace }: { workspace: Workspace } = $props()

function sourceDisplay(ws: Workspace): string {
  if (ws.source?.gitRepository) return ws.source.gitRepository
  if (ws.source?.localFolder) return ws.source.localFolder
  if (ws.source?.image) return ws.source.image
  return "Unknown source"
}

function timeAgo(timestamp?: string): string {
  if (!timestamp) return "Never"
  const diff = Date.now() - new Date(timestamp).getTime()
  const minutes = Math.floor(diff / 60000)
  if (minutes < 1) return "Just now"
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}

async function handleStop(e: Event) {
  e.stopPropagation()
  try {
    await workspaceStop(workspace.id)
    toasts.success(`Stopped ${workspace.id}`)
  } catch (err) {
    toasts.error(`Failed to stop: ${err}`)
  }
}

async function handleDelete(e: Event) {
  e.stopPropagation()
  try {
    await workspaceDelete(workspace.id)
    toasts.success(`Deleted ${workspace.id}`)
  } catch (err) {
    toasts.error(`Failed to delete: ${err}`)
  }
}
</script>

<button
  type="button"
  class="rounded-lg border bg-card p-4 text-left text-card-foreground shadow-sm transition-colors hover:bg-accent/50 w-full"
  onclick={() => goto(`/workspaces/${workspace.id}`)}
>
  <div class="flex items-start justify-between gap-2">
    <h3 class="font-semibold truncate">{workspace.id}</h3>
    <span class="text-xs text-muted-foreground whitespace-nowrap">
      {timeAgo(workspace.lastUsedTimestamp)}
    </span>
  </div>

  <p class="mt-1 text-sm text-muted-foreground truncate">
    {sourceDisplay(workspace)}
  </p>

  <div class="mt-3 flex flex-wrap items-center gap-2">
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
    {#if workspace.status}
      <span class={badgeVariants({ variant: "default" })}>
        {workspace.status}
      </span>
    {/if}
  </div>

  <div class="mt-3 flex gap-2">
    <Button variant="outline" size="sm" onclick={handleStop}>Stop</Button>
    <Button variant="destructive" size="sm" onclick={handleDelete}>Delete</Button>
  </div>
</button>
