<script lang="ts">
import { Ellipsis, Search } from "@lucide/svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { Switch } from "$lib/components/ui/switch/index.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import * as Table from "$lib/components/ui/table/index.js"
import * as DropdownMenu from "$lib/components/ui/dropdown-menu/index.js"
import * as Tooltip from "$lib/components/ui/tooltip/index.js"
import TableSkeleton from "$lib/components/ui/skeleton/TableSkeleton.svelte"
import ConfirmDialog from "$lib/components/layout/ConfirmDialog.svelte"
import {
  secrets,
  secretsError,
  secretsLoading,
  refreshSecrets,
} from "$lib/stores/secrets.js"
import { secretDelete, secretAttach, secretDetach } from "$lib/ipc/commands.js"
import { toasts } from "$lib/stores/toasts.js"
import { variableIdentity } from "$lib/variables/identity.js"
import type { Secret } from "$lib/types/index.js"
import AddSecretDialog from "./AddSecretDialog.svelte"
import SecretDetailsSheet from "./SecretDetailsSheet.svelte"
import { secretStorage, secretAvailability } from "./secret-presentation.js"
let {
  addOpen = $bindable(false),
  onManageSecurity,
}: { addOpen?: boolean; onManageSecurity?: () => void } = $props()
let search = $state("")
let busy = $state<Record<string, boolean>>({})
let resets = $state<Record<string, number>>({})
let errors = $state<Record<string, string>>({})
let selected = $state<Secret | null>(null)
let detailsOpen = $state(false)
let replaceOpen = $state(false)
let deleteOpen = $state(false)
let pendingDelete = $state<Secret | null>(null)
let deleting = $state(false)
let deleteError = $state("")
let filtered = $derived.by(() => {
  const term = search.trim().toLowerCase()
  return $secrets
    .filter(
      (s) =>
        !term ||
        s.name.toLowerCase().includes(term) ||
        s.context.toLowerCase().includes(term),
    )
    .sort(
      (a, b) =>
        a.name.localeCompare(b.name) || a.context.localeCompare(b.context),
    )
})
let currentSecret = $derived.by(() => {
  const target = selected
  if (!target) return null
  return (
    $secrets.find(
      (s) =>
        variableIdentity(s.context, s.name) ===
        variableIdentity(target.context, target.name),
    ) ?? target
  )
})
let selectedKey = $derived(
  currentSecret
    ? variableIdentity(currentSecret.context, currentSecret.name)
    : "",
)
function details(secret: Secret, replace = false) {
  selected = { ...secret }
  detailsOpen = true
  replaceOpen = replace
}
async function setAttached(secret: Secret, attached: boolean) {
  const key = variableIdentity(secret.context, secret.name)
  if (busy[key]) return
  busy = { ...busy, [key]: true }
  errors = { ...errors, [key]: "" }
  try {
    if (attached) await secretAttach(secret.name, secret.context)
    else await secretDetach(secret.name, secret.context)
    await refreshSecrets()
  } catch {
    const message = "Unable to update workspace injection. Try again."
    errors = { ...errors, [key]: message }
    toasts.error(message)
    resets = { ...resets, [key]: (resets[key] ?? 0) + 1 }
  } finally {
    busy = { ...busy, [key]: false }
  }
}
function requestDelete(secret: Secret) {
  if (deleting) return
  pendingDelete = { ...secret }
  deleteError = ""
  deleteOpen = true
}
async function remove() {
  if (!pendingDelete || deleting) return
  const target = { ...pendingDelete }
  deleting = true
  deleteError = ""
  try {
    await secretDelete(target.name, target.context)
    deleteOpen = false
    if (selectedKey === variableIdentity(target.context, target.name))
      detailsOpen = false
    toasts.success(`Secret "${target.name}" deleted`)
    await refreshSecrets()
  } catch {
    deleteError = "Unable to delete the secret. Try again."
  } finally {
    deleting = false
  }
}
</script>
<div class="space-y-4">
  <div class="flex items-center gap-3">
    <div class="relative flex-1"><Search class="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" /><Input class="pl-9" placeholder="Search secrets by name or context..." aria-label="Search secrets" bind:value={search} /></div>
    {#if onManageSecurity}<Button variant="outline" onclick={onManageSecurity}>Manage security</Button>{/if}
  </div>
  {#if $secretsLoading}<TableSkeleton columns={6} />
  {:else if $secretsError}<div class="rounded-md border p-4 space-y-3" role="alert"><p>Unable to load secrets.</p><Button variant="outline" onclick={() => refreshSecrets()}>Retry</Button></div>
  {:else if $secrets.length === 0}<div class="rounded-md border p-6 text-center"><p>No secrets stored.</p></div>
  {:else if filtered.length === 0}<p class="p-4 text-sm text-muted-foreground">No secrets match "{search}"</p>
  {:else}
    <div class="overflow-x-auto rounded-md border"><Table.Root>
      <Table.Header><Table.Row>
        <Table.Head class="min-w-60">Name</Table.Head><Table.Head class="min-w-35">Context</Table.Head><Table.Head class="min-w-35">Storage</Table.Head><Table.Head class="min-w-37">Status</Table.Head>
        <Table.Head class="min-w-22"><Tooltip.Provider><Tooltip.Root><Tooltip.Trigger>Inject</Tooltip.Trigger><Tooltip.Content>Inject automatically into workspace setup and new Devsy-managed terminal/SSH sessions. Changes apply on the next applicable workspace start/recreation or new session.</Tooltip.Content></Tooltip.Root></Tooltip.Provider></Table.Head>
        <Table.Head><span class="sr-only">Actions</span></Table.Head>
      </Table.Row></Table.Header>
      <Table.Body>{#each filtered as secret (variableIdentity(secret.context, secret.name))}
        {@const key = variableIdentity(secret.context, secret.name)}
        <Table.Row class="cursor-pointer" onclick={() => details(secret)}>
          <Table.Cell class="font-medium"><button class="text-left hover:underline" onclick={(e) => { e.stopPropagation(); details(secret) }}>{secret.name}</button></Table.Cell>
          <Table.Cell>{secret.context}</Table.Cell><Table.Cell>{secretStorage(secret)}</Table.Cell>
          <Table.Cell><span class={badgeVariants({ variant: secret.availability === "missing" || secret.availability === "backend_unavailable" ? "destructive" : "secondary" })}>{secretAvailability(secret)}</span></Table.Cell>
          <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
          <Table.Cell onclick={(e: MouseEvent) => e.stopPropagation()}>
            {#key resets[key] ?? 0}<Switch checked={secret.attached ?? false} disabled={busy[key]} aria-label={`Inject ${secret.name} into workspaces`} onCheckedChange={(checked) => setAttached(secret, checked)} />{/key}
            {#if errors[key]}<p class="mt-2 text-sm text-destructive" role="alert">{errors[key]}</p>{/if}
          </Table.Cell>
          <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
          <Table.Cell onclick={(e: MouseEvent) => e.stopPropagation()}><DropdownMenu.Root>
            <DropdownMenu.Trigger>{#snippet child({ props })}<Button {...props} variant="ghost" size="icon" aria-label={`Actions for ${secret.name}`}><Ellipsis class="size-4" /></Button>{/snippet}</DropdownMenu.Trigger>
            <DropdownMenu.Content align="end"><DropdownMenu.Item onclick={() => details(secret)}>View details</DropdownMenu.Item><DropdownMenu.Item onclick={() => details(secret, true)}>Replace value</DropdownMenu.Item><DropdownMenu.Separator /><DropdownMenu.Item class="text-destructive" onclick={() => requestDelete(secret)}>Delete</DropdownMenu.Item></DropdownMenu.Content>
          </DropdownMenu.Root></Table.Cell>
        </Table.Row>
      {/each}</Table.Body>
    </Table.Root></div>
  {/if}
</div>
<AddSecretDialog bind:open={addOpen} />
<SecretDetailsSheet bind:open={detailsOpen} bind:replaceOpen secret={currentSecret} attachmentBusy={busy[selectedKey]} attachmentError={errors[selectedKey]} attachmentReset={resets[selectedKey]} onAttachmentChange={setAttached} />
<ConfirmDialog bind:open={() => deleteOpen, (next) => { if (!deleting) deleteOpen = next }} title="Delete secret" description={`Permanently delete secret '${pendingDelete?.name ?? ""}' in ${pendingDelete?.context ?? ""} and its value. This cannot be undone.`} confirmLabel="Delete" loading={deleting} onconfirm={remove}>{#if deleteError}<p class="text-sm text-destructive" role="alert">{deleteError}</p>{/if}</ConfirmDialog>
