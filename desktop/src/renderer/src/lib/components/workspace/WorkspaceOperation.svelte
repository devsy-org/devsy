<script lang="ts">
import { workspaceJobs } from "$lib/stores/workspaces.js"
import { toasts } from "$lib/stores/toasts.js"
import { extractErrorMessage } from "$lib/utils/error.js"
import { workspaceRefresh } from "$lib/ipc/commands.js"
import { Loader2 } from "@lucide/svelte"
import {
  presentWorkspaceStatus,
  type WorkspaceJob,
} from "$shared/workspace-operation.js"
import { goto } from "$lib/router.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"

let {
  id,
  status,
  job: jobOverride,
  density = "compact",
  onViewLogs,
}: {
  id: string
  status?: string
  job?: WorkspaceJob
  density?: "compact" | "expanded"
  onViewLogs?: () => void
} = $props()
let job = $derived(jobOverride ?? $workspaceJobs[id])
let view = $derived(presentWorkspaceStatus({ lifecycle: status, job }))
let refreshing = $state(false)
function viewLogs(event: MouseEvent) {
  event.stopPropagation()
  if (onViewLogs) onViewLogs()
  else goto(`/workspaces/${id}?tab=logs`)
}
async function retryRefresh(event: MouseEvent) {
  event.stopPropagation()
  refreshing = true
  try {
    await workspaceRefresh(id)
  } catch (error) {
    toasts.error(
      `Could not refresh workspace status: ${extractErrorMessage(error)}`,
    )
  } finally {
    refreshing = false
  }
}
const badgeVariant = $derived(
  view.tone === "warning"
    ? "secondary"
    : (view.tone as "default" | "secondary" | "outline" | "destructive"),
)
</script>

<div role="status" aria-live={view.error ? "assertive" : "polite"} aria-busy={view.busy} class="flex min-h-10 flex-col items-start gap-1">
  <span class={badgeVariants({ variant: badgeVariant })}>
    {#if view.busy}<Loader2 class="size-3 animate-spin" aria-hidden="true" />{/if}
    {view.headline}
  </span>
  <span
    class="max-w-full truncate text-xs {view.error
      ? 'text-destructive'
      : view.recovery
        ? 'text-amber-600 dark:text-amber-400'
        : 'text-muted-foreground'}"
    title={view.error ?? view.recovery?.message ?? view.phase ?? undefined}
  >
    {#if view.error}
      {view.error}{#if density === "expanded"}{" · "}<button
        type="button"
        class="font-medium text-foreground underline underline-offset-2"
        aria-label="View logs for {id}"
        onclick={viewLogs}>View logs</button>{/if}
    {:else if view.recovery}
      &#9888; {view.recovery.message}{#if density === "expanded" && view.recovery.canRetry}{" · "}<button
          type="button"
          class="font-medium text-foreground underline underline-offset-2"
          aria-label="Retry status for {id}"
          disabled={refreshing}
          onclick={retryRefresh}>Retry</button
        >{/if}
    {:else if view.phase}
      {view.phase}
    {:else}
      <span aria-hidden="true">&nbsp;</span>
    {/if}
  </span>
</div>
