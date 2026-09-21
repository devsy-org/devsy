<script lang="ts">
import { workspaceJobs } from "$lib/stores/workspaces.js"
import { toasts } from "$lib/stores/toasts.js"
import { extractErrorMessage } from "$lib/utils/error.js"
import { workspaceRefresh } from "$lib/ipc/commands.js"
import { Spinner } from "$lib/components/ui/spinner/index.js"
import { workspaceJobBusy, workspaceJobLabel, workspaceJobPhase } from "$shared/workspace-operation.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import { Button } from "$lib/components/ui/button/index.js"
let { id, status }: { id: string; status?: string } = $props()
let job = $derived($workspaceJobs[id])
let label = $derived(workspaceJobLabel(job))
let phase = $derived(workspaceJobPhase(job))
let refreshing = $state(false)
async function retryRefresh(event: MouseEvent) {
  event.stopPropagation()
  refreshing = true
  try {
    await workspaceRefresh(id)
  } catch (error) {
    toasts.error(`Could not refresh workspace status: ${extractErrorMessage(error)}`)
  } finally {
    refreshing = false
  }
}
</script>
<div aria-live="polite" aria-busy={workspaceJobBusy(job)} class="flex flex-col items-start gap-1">
  <span class={badgeVariants({ variant: job?.error ? "destructive" : label ? "secondary" : status?.toLowerCase() === "running" ? "default" : "outline" })}>
    {#if workspaceJobBusy(job)}<Spinner class="size-3" />{/if}
    {label ?? status ?? "Checking"}
  </span>
  {#if phase}<span class="text-xs text-muted-foreground">{phase}</span>{/if}
  {#if job?.error}<span class="text-xs text-destructive">{job.error}</span>{/if}
  {#if job?.refreshError}
    <Button variant="ghost" size="sm" disabled={refreshing} onclick={retryRefresh}>Retry refresh</Button>
  {/if}
</div>
