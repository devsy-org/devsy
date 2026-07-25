<script lang="ts">
import { Braces, Eye, EyeOff, Plus, Search, Trash2 } from "@lucide/svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { Label } from "$lib/components/ui/label/index.js"
import * as Dialog from "$lib/components/ui/dialog/index.js"
import ConfirmDialog from "$lib/components/layout/ConfirmDialog.svelte"
import CardSkeleton from "$lib/components/ui/skeleton/CardSkeleton.svelte"
import { envVars, envLoading, refreshEnv } from "$lib/stores/env.js"
import { envSet, envDelete } from "$lib/ipc/commands.js"
import { toasts } from "$lib/stores/toasts.js"
import { extractErrorMessage } from "$lib/utils/error.js"

const NAME_PATTERN = /^[A-Za-z_][A-Za-z0-9_]*$/

let createDialogOpen = $state(false)
let newName = $state("")
let newValue = $state("")
let saving = $state(false)
let revealed = $state<Record<string, boolean>>({})

let confirmDeleteOpen = $state(false)
let pendingDelete = $state("")
let deleting = $state(false)

let searchTerm = $state("")
let filteredEnvVars = $derived.by(() => {
  const q = searchTerm.trim().toLowerCase()
  const list = q
    ? $envVars.filter((e) => e.name.toLowerCase().includes(q))
    : [...$envVars]
  list.sort((a, b) => a.name.localeCompare(b.name))
  return list
})

let nameValid = $derived(NAME_PATTERN.test(newName))
let nameExists = $derived($envVars.some((e) => e.name === newName.trim()))

$effect(() => {
  if (!createDialogOpen) {
    newName = ""
    newValue = ""
  }
})

async function handleCreate() {
  const name = newName.trim()
  if (!name || !nameValid) return
  const updating = nameExists
  saving = true
  try {
    await envSet(name, newValue)
  } catch (err) {
    toasts.error(`Failed to save environment variable: ${extractErrorMessage(err)}`)
    saving = false
    return
  }
  createDialogOpen = false
  toasts.success(
    updating
      ? `Environment variable "${name}" updated`
      : `Environment variable "${name}" saved`,
  )
  saving = false
  await refreshEnv().catch(() => {})
}

function requestDelete(e: Event, name: string) {
  e.stopPropagation()
  pendingDelete = name
  confirmDeleteOpen = true
}

async function confirmDelete() {
  const name = pendingDelete
  deleting = true
  try {
    await envDelete(name)
  } catch (err) {
    toasts.error(`Failed to delete environment variable: ${extractErrorMessage(err)}`)
    deleting = false
    return
  }
  confirmDeleteOpen = false
  toasts.success(`Environment variable "${name}" deleted`)
  deleting = false
  await refreshEnv().catch(() => {})
}

function toggleReveal(name: string) {
  revealed = { ...revealed, [name]: !revealed[name] }
}
</script>

<div class="space-y-6">
  <div class="flex items-center justify-between">
    <h1 class="text-2xl font-bold">Environment Variables</h1>
    <Dialog.Root bind:open={createDialogOpen}>
      <Dialog.Trigger>
        {#snippet child({ props })}
          <Button size="sm" {...props}>
            <Plus class="mr-2 h-4 w-4" />
            Add Variable
          </Button>
        {/snippet}
      </Dialog.Trigger>
      <Dialog.Content class="sm:max-w-md">
        <Dialog.Header>
          <Dialog.Title>
            {nameExists ? "Update Environment Variable" : "Add Environment Variable"}
          </Dialog.Title>
          <Dialog.Description>
            Stored in plaintext in your Devsy config and injected into workspaces.
          </Dialog.Description>
        </Dialog.Header>
        <form onsubmit={(e) => { e.preventDefault(); handleCreate() }} class="space-y-4">
          <div class="space-y-1.5">
            <Label for="env-name">Name</Label>
            <Input
              id="env-name"
              placeholder="e.g. LOG_LEVEL"
              value={newName}
              oninput={(e) => (newName = e.currentTarget.value)}
              disabled={saving}
            />
            {#if newName && !nameValid}
              <p class="text-sm text-destructive">
                Only letters, digits, and underscores are allowed.
              </p>
            {:else if nameExists}
              <p class="text-sm text-muted-foreground">
                "{newName.trim()}" already exists. Saving will update its value.
              </p>
            {/if}
          </div>
          <div class="space-y-1.5">
            <Label for="env-value">Value</Label>
            <Input
              id="env-value"
              type="text"
              placeholder="Value"
              value={newValue}
              oninput={(e) => (newValue = e.currentTarget.value)}
              disabled={saving}
            />
          </div>
          <Dialog.Footer>
            <Button type="button" variant="outline" onclick={() => (createDialogOpen = false)} disabled={saving}>Cancel</Button>
            <Button type="submit" disabled={saving || !newName.trim() || !nameValid}>
              {saving ? "Saving..." : nameExists ? "Update" : "Save"}
            </Button>
          </Dialog.Footer>
        </form>
      </Dialog.Content>
    </Dialog.Root>
  </div>

  {#if $envLoading}
    <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
      {#each Array(2) as _}
        <CardSkeleton />
      {/each}
    </div>
  {:else if $envVars.length === 0}
    <div class="flex flex-col items-center justify-center gap-4 py-16 text-center">
      <Braces class="h-10 w-10 text-muted-foreground" />
      <p class="text-muted-foreground">No environment variables stored.</p>
    </div>
  {:else}
    <div class="relative">
      <Search class="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        placeholder="Filter environment variables..."
        class="pl-9"
        value={searchTerm}
        oninput={(e) => (searchTerm = e.currentTarget.value)}
      />
    </div>
    {#if filteredEnvVars.length === 0}
      <div class="flex flex-col items-center justify-center gap-4 py-16 text-center">
        <Search class="h-10 w-10 text-muted-foreground" />
        <p class="text-muted-foreground">No environment variables matching "{searchTerm}"</p>
      </div>
    {:else}
      <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {#each filteredEnvVars as envVar (envVar.name)}
          <div class="rounded-xl border bg-card p-6 text-card-foreground shadow-sm">
            <div class="flex items-start justify-between gap-3">
              <div class="flex items-center gap-3 min-w-0">
                <Braces class="size-8 shrink-0 text-muted-foreground" />
                <h3 class="text-lg font-semibold truncate">{envVar.name}</h3>
              </div>
              <div class="flex items-center gap-1.5 shrink-0">
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={revealed[envVar.name] ? "Hide value" : "Show value"}
                  onclick={() => toggleReveal(envVar.name)}
                >
                  {#if revealed[envVar.name]}
                    <EyeOff class="h-4 w-4" />
                  {:else}
                    <Eye class="h-4 w-4" />
                  {/if}
                </Button>
                <Button variant="ghost" size="icon" aria-label="Delete environment variable" onclick={(e) => requestDelete(e, envVar.name)}>
                  <Trash2 class="h-4 w-4" />
                </Button>
              </div>
            </div>
            <p class="mt-2 font-mono text-sm text-muted-foreground break-all">
              {revealed[envVar.name] ? envVar.value : "••••••••"}
            </p>
          </div>
        {/each}
      </div>
    {/if}
  {/if}
</div>

<ConfirmDialog
  bind:open={confirmDeleteOpen}
  title="Delete environment variable"
  description="This deletes environment variable '{pendingDelete}'. Workspaces that inject it will no longer receive it."
  confirmLabel="Delete"
  loading={deleting}
  onconfirm={confirmDelete}
/>
