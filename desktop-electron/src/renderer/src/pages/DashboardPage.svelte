<script lang="ts">
import { Box, Plug, Server } from "@lucide/svelte"
import { goto } from "$lib/router.js"
import { Button } from "$lib/components/ui/button/index.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import { workspaces } from "$lib/stores/workspaces.js"
import { providers } from "$lib/stores/providers.js"
import { machines } from "$lib/stores/machines.js"
import { activeContext } from "$lib/stores/contexts.js"
import { workspaceStop } from "$lib/ipc/commands.js"
import { toasts } from "$lib/stores/toasts.js"

let runningWorkspaces = $derived(
  $workspaces.filter((ws) => ws.status?.toLowerCase() === "running"),
)
let runningMachines = $derived(
  $machines.filter((m) => m.status?.toLowerCase() === "running"),
)

const stats = $derived([
  {
    label: "Workspaces",
    count: $workspaces.length,
    href: "/workspaces",
    icon: Box,
    sub:
      runningWorkspaces.length > 0
        ? `${runningWorkspaces.length} running`
        : undefined,
  },
  {
    label: "Providers",
    count: $providers.length,
    href: "/providers",
    icon: Plug,
    sub: undefined as string | undefined,
  },
  {
    label: "Machines",
    count: $machines.length,
    href: "/machines",
    icon: Server,
    sub:
      runningMachines.length > 0
        ? `${runningMachines.length} running`
        : undefined,
  },
])

async function quickStop(wsId: string) {
  try {
    await workspaceStop(wsId)
    toasts.success(`Stopping ${wsId}...`)
  } catch (err) {
    toasts.error(`Failed to stop: ${err}`)
  }
}
</script>

<div class="space-y-6">
  <div>
    <h1 class="text-2xl font-bold">Dashboard</h1>
    {#if $activeContext}
      <p class="mt-1 text-sm text-muted-foreground">
        Context: <span class="font-medium">{$activeContext}</span>
      </p>
    {/if}
  </div>

  <div class="grid gap-4 sm:grid-cols-3">
    {#each stats as stat (stat.label)}
      {@const Icon = stat.icon}
      <button
        type="button"
        class="rounded-lg border bg-card p-6 text-left text-card-foreground shadow-sm transition-colors hover:bg-accent/50"
        onclick={() => goto(stat.href)}
      >
        <div class="flex items-center justify-between">
          <div class="text-3xl font-bold">{stat.count}</div>
          <Icon class="h-5 w-5 text-muted-foreground" />
        </div>
        <div class="mt-1 text-sm text-muted-foreground">{stat.label}</div>
        {#if stat.sub}
          <div class="mt-1 text-xs text-green-600 dark:text-green-400">{stat.sub}</div>
        {/if}
      </button>
    {/each}
  </div>

  <div class="flex gap-2">
    <Button onclick={() => goto("/workspaces?create=true")}>New Workspace</Button>
    <Button variant="outline" onclick={() => goto("/providers/add")}>Add Provider</Button>
  </div>

  {#if runningWorkspaces.length > 0}
    <div class="space-y-3">
      <h2 class="text-lg font-semibold">Active Workspaces</h2>
      <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {#each runningWorkspaces as ws (ws.id)}
          <div class="rounded-lg border bg-card p-4 shadow-sm">
            <div class="flex items-start justify-between gap-2">
              <button
                class="min-w-0 truncate font-medium hover:underline text-left"
                onclick={() => goto(`/workspaces/${ws.id}`)}
              >
                {ws.id}
              </button>
              <div class="flex shrink-0 gap-1">
                <Button variant="outline" size="sm" onclick={() => goto(`/workspaces/${ws.id}`)}>
                  Open
                </Button>
                <Button variant="ghost" size="sm" onclick={() => quickStop(ws.id)}>
                  Stop
                </Button>
              </div>
            </div>
            <div class="flex items-center gap-2 mt-1">
              {#if ws.provider?.name}
                <span class="text-xs text-muted-foreground">{ws.provider.name}</span>
              {/if}
              <span class={badgeVariants({ variant: "default" })}>{ws.status}</span>
            </div>
          </div>
        {/each}
      </div>
    </div>
  {/if}
</div>
