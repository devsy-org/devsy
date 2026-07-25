<script lang="ts">
import { KeyRound, Plus, Search, Trash2 } from "@lucide/svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { Label } from "$lib/components/ui/label/index.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import * as Dialog from "$lib/components/ui/dialog/index.js"
import ConfirmDialog from "$lib/components/layout/ConfirmDialog.svelte"
import CardSkeleton from "$lib/components/ui/skeleton/CardSkeleton.svelte"
import { secrets, secretsLoading, refreshSecrets } from "$lib/stores/secrets.js"
import { secretSet, secretDelete } from "$lib/ipc/commands.js"
import { toasts } from "$lib/stores/toasts.js"
import { extractErrorMessage } from "$lib/utils/error.js"

const NAME_PATTERN = /^[A-Za-z_][A-Za-z0-9_]*$/

let createDialogOpen = $state(false)
let newName = $state("")
let newValue = $state("")
let saving = $state(false)

let confirmDeleteOpen = $state(false)
let pendingDelete = $state("")
let deleting = $state(false)

let searchTerm = $state("")
let filteredSecrets = $derived.by(() => {
  const q = searchTerm.trim().toLowerCase()
  const list = q
    ? $secrets.filter((s) => s.name.toLowerCase().includes(q))
    : [...$secrets]
  list.sort((a, b) => a.name.localeCompare(b.name))
  return list
})

let nameValid = $derived(NAME_PATTERN.test(newName))
let nameExists = $derived(
  $secrets.some((s) => s.name === newName.trim()),
)

$effect(() => {
  if (!createDialogOpen) {
    newName = ""
    newValue = ""
  }
})

async function handleCreate() {
  const name = newName.trim()
  if (!name || !nameValid || !newValue) return
  const replacing = nameExists
  saving = true
  try {
    await secretSet(name, newValue)
  } catch (err) {
    toasts.error(`Failed to save secret: ${extractErrorMessage(err)}`)
    saving = false
    return
  }
  createDialogOpen = false
  toasts.success(replacing ? `Secret "${name}" replaced` : `Secret "${name}" saved`)
  saving = false
  await refreshSecrets().catch(() => {})
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
    await secretDelete(name)
  } catch (err) {
    toasts.error(`Failed to delete secret: ${extractErrorMessage(err)}`)
    deleting = false
    return
  }
  confirmDeleteOpen = false
  toasts.success(`Secret "${name}" deleted`)
  deleting = false
  await refreshSecrets().catch(() => {})
}
</script>

<div class="space-y-6">
  <div class="flex items-center justify-between">
    <h1 class="text-2xl font-bold">Secrets</h1>
    <Dialog.Root bind:open={createDialogOpen}>
      <Dialog.Trigger>
        {#snippet child({ props })}
          <Button size="sm" {...props}>
            <Plus class="mr-2 h-4 w-4" />
            Add Secret
          </Button>
        {/snippet}
      </Dialog.Trigger>
      <Dialog.Content class="sm:max-w-md">
        <Dialog.Header>
          <Dialog.Title>{nameExists ? "Replace Secret" : "Add Secret"}</Dialog.Title>
          <Dialog.Description>
            Stored in your OS keyring and injected into workspaces on demand.
          </Dialog.Description>
        </Dialog.Header>
        <form onsubmit={(e) => { e.preventDefault(); handleCreate() }} class="space-y-4">
          <div class="space-y-1.5">
            <Label for="secret-name">Name</Label>
            <Input
              id="secret-name"
              placeholder="e.g. DB_PASSWORD"
              value={newName}
              oninput={(e) => (newName = e.currentTarget.value)}
              disabled={saving}
            />
            {#if newName && !nameValid}
              <p class="text-sm text-destructive">
                Only letters, digits, and underscores are allowed.
              </p>
            {:else if nameExists}
              <p class="text-sm text-amber-600 dark:text-amber-500">
                A secret named "{newName.trim()}" already exists. Saving will
                replace its value. This cannot be undone.
              </p>
            {/if}
          </div>
          <div class="space-y-1.5">
            <Label for="secret-value">Value</Label>
            <Input
              id="secret-value"
              type="password"
              placeholder="Secret value"
              value={newValue}
              oninput={(e) => (newValue = e.currentTarget.value)}
              disabled={saving}
            />
          </div>
          <Dialog.Footer>
            <Button type="button" variant="outline" onclick={() => (createDialogOpen = false)} disabled={saving}>Cancel</Button>
            <Button
              type="submit"
              variant={nameExists ? "destructive" : "default"}
              disabled={saving || !newName.trim() || !nameValid || !newValue}
            >
              {saving ? "Saving..." : nameExists ? "Replace" : "Save"}
            </Button>
          </Dialog.Footer>
        </form>
      </Dialog.Content>
    </Dialog.Root>
  </div>

  {#if $secretsLoading}
    <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
      {#each Array(2) as _}
        <CardSkeleton />
      {/each}
    </div>
  {:else if $secrets.length === 0}
    <div class="flex flex-col items-center justify-center gap-4 py-16 text-center">
      <KeyRound class="h-10 w-10 text-muted-foreground" />
      <p class="text-muted-foreground">No secrets stored.</p>
    </div>
  {:else}
    <div class="relative">
      <Search class="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        placeholder="Filter secrets..."
        class="pl-9"
        value={searchTerm}
        oninput={(e) => (searchTerm = e.currentTarget.value)}
      />
    </div>
    {#if filteredSecrets.length === 0}
      <div class="flex flex-col items-center justify-center gap-4 py-16 text-center">
        <Search class="h-10 w-10 text-muted-foreground" />
        <p class="text-muted-foreground">No secrets matching "{searchTerm}"</p>
      </div>
    {:else}
      <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {#each filteredSecrets as secret (secret.name)}
          <div class="rounded-xl border bg-card p-6 text-card-foreground shadow-sm">
            <div class="flex items-start justify-between gap-3">
              <div class="flex items-center gap-3 min-w-0">
                <KeyRound class="size-8 shrink-0 text-muted-foreground" />
                <h3 class="text-lg font-semibold truncate">{secret.name}</h3>
              </div>
              <div class="flex items-center gap-1.5 shrink-0">
                {#if secret.orphaned}
                  <span class={badgeVariants({ variant: "destructive" })}>orphaned</span>
                {/if}
                <Button variant="ghost" size="icon" aria-label="Delete secret" onclick={(e) => requestDelete(e, secret.name)}>
                  <Trash2 class="h-4 w-4" />
                </Button>
              </div>
            </div>
            <p class="mt-2 text-sm text-muted-foreground">
              Context: {secret.context}
            </p>
          </div>
        {/each}
      </div>
    {/if}
  {/if}
</div>

<ConfirmDialog
  bind:open={confirmDeleteOpen}
  title="Delete secret"
  description="This permanently deletes secret '{pendingDelete}' and its value. Workspaces that inject it will no longer receive it. This cannot be undone."
  confirmLabel="Delete"
  loading={deleting}
  onconfirm={confirmDelete}
/>
