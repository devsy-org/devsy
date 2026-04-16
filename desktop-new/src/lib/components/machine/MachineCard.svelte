<script lang="ts">
import { goto } from "$app/navigation"
import { Button } from "$lib/components/ui/button/index.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import { machineStop, machineDelete } from "$lib/ipc/commands.js"
import { toasts } from "$lib/stores/toasts.js"
import type { Machine } from "$lib/types/index.js"

let { machine }: { machine: Machine } = $props()

function timeAgo(timestamp?: string): string {
  if (!timestamp) return "Unknown"
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
    await machineStop(machine.id)
    toasts.success(`Stopped ${machine.id}`)
  } catch (err) {
    toasts.error(`Failed to stop: ${err}`)
  }
}

async function handleDelete(e: Event) {
  e.stopPropagation()
  try {
    await machineDelete(machine.id)
    toasts.success(`Deleted ${machine.id}`)
  } catch (err) {
    toasts.error(`Failed to delete: ${err}`)
  }
}
</script>

<button
  type="button"
  class="rounded-lg border bg-card p-4 text-left text-card-foreground shadow-sm transition-colors hover:bg-accent/50 w-full"
  onclick={() => goto(`/machines/${machine.id}`)}
>
  <div class="flex items-start justify-between gap-2">
    <h3 class="font-semibold truncate">{machine.id}</h3>
    {#if machine.status}
      <span class={badgeVariants({ variant: "default" })}>{machine.status}</span>
    {/if}
  </div>

  <div class="mt-2 flex flex-wrap items-center gap-2">
    {#if machine.provider?.name}
      <span class={badgeVariants({ variant: "secondary" })}>{machine.provider.name}</span>
    {/if}
    <span class="text-xs text-muted-foreground">
      Created {timeAgo(machine.creationTimestamp)}
    </span>
  </div>

  <div class="mt-3 flex gap-2">
    <Button variant="outline" size="sm" onclick={handleStop}>Stop</Button>
    <Button variant="destructive" size="sm" onclick={handleDelete}>Delete</Button>
  </div>
</button>
