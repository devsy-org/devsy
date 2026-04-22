<script lang="ts">
import { Layers, Plus, Settings2 } from "@lucide/svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { Label } from "$lib/components/ui/label/index.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import * as Dialog from "$lib/components/ui/dialog/index.js"
import CardSkeleton from "$lib/components/ui/skeleton/CardSkeleton.svelte"
import ContextSheet from "$lib/components/context/ContextSheet.svelte"
import {
  contexts,
  activeContext,
  contextsLoading,
  refreshContexts,
} from "$lib/stores/contexts.js"
import { contextUse, contextCreate } from "$lib/ipc/commands.js"
import { toasts } from "$lib/stores/toasts.js"

let selectedContext = $state<string | null>(null)
let sheetOpen = $state(false)

let createDialogOpen = $state(false)
let newContextName = $state("")
let creating = $state(false)

let selectedCtx = $derived(
  selectedContext
    ? $contexts.find((c) => c.name === selectedContext)
    : undefined,
)

function openSheet(name: string) {
  selectedContext = name
  sheetOpen = true
}

async function handleUse(e: Event, name: string) {
  e.stopPropagation()
  try {
    await contextUse(name)
    toasts.success(`Switched to context "${name}"`)
  } catch (err) {
    toasts.error(`Failed to switch context: ${err}`)
  }
}

async function handleCreate() {
  const name = newContextName.trim()
  if (!name) return
  creating = true
  try {
    await contextCreate(name)
    await refreshContexts()
    toasts.success(`Context "${name}" created`)
    createDialogOpen = false
    newContextName = ""
  } catch (err) {
    toasts.error(`Failed to create context: ${err}`)
  } finally {
    creating = false
  }
}
</script>

<div class="space-y-6">
  <div class="flex items-center justify-between">
    <h1 class="text-2xl font-bold">Contexts</h1>
    <Dialog.Root bind:open={createDialogOpen}>
      <Dialog.Trigger>
        {#snippet child({ props })}
          <Button size="sm" {...props}>
            <Plus class="mr-2 h-4 w-4" />
            Create Context
          </Button>
        {/snippet}
      </Dialog.Trigger>
      <Dialog.Content class="sm:max-w-md">
        <Dialog.Header>
          <Dialog.Title>Create Context</Dialog.Title>
          <Dialog.Description>Create a new context to organize your workspace configurations.</Dialog.Description>
        </Dialog.Header>
        <form onsubmit={(e) => { e.preventDefault(); handleCreate() }} class="space-y-4">
          <div class="space-y-1.5">
            <Label>Name</Label>
            <Input
              placeholder="e.g. staging, production"
              value={newContextName}
              oninput={(e) => (newContextName = e.currentTarget.value)}
              disabled={creating}
            />
          </div>
          <Dialog.Footer>
            <Button type="button" variant="outline" onclick={() => (createDialogOpen = false)} disabled={creating}>Cancel</Button>
            <Button type="submit" disabled={creating || !newContextName.trim()}>
              {creating ? "Creating..." : "Create"}
            </Button>
          </Dialog.Footer>
        </form>
      </Dialog.Content>
    </Dialog.Root>
  </div>

  {#if $contextsLoading}
    <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {#each Array(2) as _}
        <CardSkeleton />
      {/each}
    </div>
  {:else if $contexts.length === 0}
    <div class="flex flex-col items-center justify-center gap-4 py-16 text-center">
      <Layers class="h-10 w-10 text-muted-foreground" />
      <p class="text-muted-foreground">No contexts found.</p>
    </div>
  {:else}
    <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {#each $contexts as ctx (ctx.name)}
        <button
          type="button"
          class="rounded-xl border bg-card p-6 text-left text-card-foreground shadow-sm transition-colors hover:bg-accent/50 w-full {ctx.name === $activeContext ? 'ring-2 ring-primary' : ''}"
          onclick={() => openSheet(ctx.name)}
        >
          <div class="flex items-start justify-between gap-2">
            <div class="flex items-center gap-2 min-w-0">
              <h3 class="text-lg font-semibold truncate">{ctx.name}</h3>
              {#if ctx.name === $activeContext}
                <span class={badgeVariants({ variant: "default" })}>active</span>
              {/if}
            </div>
            <Settings2 class="h-4 w-4 shrink-0 text-muted-foreground mt-1" />
          </div>

          {#if ctx.options && Object.keys(ctx.options).length > 0}
            <div class="mt-3 space-y-1">
              {#each Object.entries(ctx.options).slice(0, 4) as [key, value]}
                <div class="text-xs text-muted-foreground truncate">
                  <span class="font-medium">{key}:</span>
                  {value}
                </div>
              {/each}
              {#if Object.keys(ctx.options).length > 4}
                <p class="text-xs text-muted-foreground">+{Object.keys(ctx.options).length - 4} more</p>
              {/if}
            </div>
          {:else}
            <p class="mt-3 text-sm text-muted-foreground">Click to configure options</p>
          {/if}

          {#if ctx.name !== $activeContext}
            <div class="mt-4">
              <Button variant="outline" size="sm" onclick={(e) => handleUse(e, ctx.name)}>
                Set Active
              </Button>
            </div>
          {/if}
        </button>
      {/each}
    </div>
  {/if}
</div>

{#if selectedCtx}
  <ContextSheet
    context={selectedCtx}
    isActive={selectedCtx.name === $activeContext}
    bind:open={sheetOpen}
  />
{/if}
