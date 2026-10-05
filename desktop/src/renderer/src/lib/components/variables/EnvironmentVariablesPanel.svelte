<script lang="ts">
import { Eye, EyeOff, Ellipsis } from "@lucide/svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { Switch } from "$lib/components/ui/switch/index.js"
import * as Table from "$lib/components/ui/table/index.js"
import * as DropdownMenu from "$lib/components/ui/dropdown-menu/index.js"
import TableSkeleton from "$lib/components/ui/skeleton/TableSkeleton.svelte"
import ConfirmDialog from "$lib/components/layout/ConfirmDialog.svelte"
import AddEnvironmentVariableDialog from "./AddEnvironmentVariableDialog.svelte"
import EnvironmentVariableDetailsSheet from "./EnvironmentVariableDetailsSheet.svelte"
import {
  envVars,
  envLoading,
  envError,
  refreshEnv,
  initEnv,
} from "$lib/stores/env.js"
import { envDelete, envAttach, envDetach } from "$lib/ipc/commands.js"
import { toasts } from "$lib/stores/toasts.js"
import { variableIdentity } from "$lib/variables/identity.js"
import type { EnvVar } from "$lib/types/index.js"
let { addOpen = $bindable(false) }: { addOpen?: boolean } = $props()
let search = $state("")
let revealed = $state<Record<string, boolean>>({})
let updating = $state<Record<string, boolean>>({})
let errors = $state<Record<string, string>>({})
let resets = $state<Record<string, number>>({})
let detailsOpen = $state(false)
let selected = $state<EnvVar | null>(null)
let deleteOpen = $state(false)
let pendingDelete = $state<EnvVar | null>(null)
let deleting = $state(false)
let deleteError = $state("")
const rows = $derived(
  [...$envVars]
    .filter((row) =>
      `${row.name} ${row.context}`
        .toLowerCase()
        .includes(search.trim().toLowerCase()),
    )
    .sort(
      (a, b) =>
        a.name.localeCompare(b.name) || a.context.localeCompare(b.context),
    ),
)
const selectedKey = $derived(
  selected ? variableIdentity(selected.context, selected.name) : "",
)
const selectedRow = $derived(
  $envVars.find(
    (row) => variableIdentity(row.context, row.name) === selectedKey,
  ) ?? selected,
)
function showDetails(row: EnvVar) {
  selected = { ...row }
  detailsOpen = true
}
function requestDelete(row: EnvVar) {
  pendingDelete = { ...row }
  deleteError = ""
  deleteOpen = true
  detailsOpen = false
}
async function remove() {
  const target = pendingDelete
  if (!target || deleting) return
  deleting = true
  try {
    await envDelete(target.name, target.context)
    deleteOpen = false
    pendingDelete = null
    toasts.success(`Environment variable "${target.name}" deleted`)
    await refreshEnv()
  } catch {
    deleteError =
      "Unable to delete variable. Check your connection and try again."
  } finally {
    deleting = false
  }
}
async function setAttached(row: EnvVar, attached: boolean) {
  const key = variableIdentity(row.context, row.name)
  if (updating[key]) return
  updating = { ...updating, [key]: true }
  errors = { ...errors, [key]: "" }
  try {
    if (attached) await envAttach(row.name, row.context)
    else await envDetach(row.name, row.context)
    await refreshEnv()
  } catch {
    toasts.error("Unable to update workspace injection. Try again.")
    errors = {
      ...errors,
      [key]: "Workspace injection could not be updated. Try again.",
    }
  } finally {
    resets = { ...resets, [key]: (resets[key] ?? 0) + 1 }
    updating = { ...updating, [key]: false }
  }
}
</script>
<div class="space-y-4">
  <p class="rounded-md border bg-muted/30 px-4 py-3 text-sm text-muted-foreground">Environment variables are stored as non-sensitive plaintext configuration. Use Secrets for passwords, tokens, and other credentials.</p>
  <Input aria-label="Search environment variables" placeholder="Search name or context…" bind:value={search} />
  {#if $envLoading}<TableSkeleton columns={5} />
  {:else if $envError}<div role="alert" class="space-y-2"><p>Unable to load environment variables.</p><Button variant="outline" onclick={() => initEnv()}>Retry</Button></div>
  {:else if $envVars.length === 0}<div class="space-y-3 py-8 text-center"><p>No environment variables yet.</p></div>
  {:else if rows.length === 0}<p class="py-4 text-sm text-muted-foreground">No environment variables match "{search}"</p>
  {:else}
    <Table.Root>
      <Table.Header><Table.Row><Table.Head>Name</Table.Head><Table.Head>Context</Table.Head><Table.Head>Value</Table.Head><Table.Head title="Inject automatically into workspace setup and new Devsy-managed terminal/SSH sessions. Changes apply on the next applicable workspace start/recreation or new session.">Inject</Table.Head><Table.Head><span class="sr-only">Actions</span></Table.Head></Table.Row></Table.Header>
      <Table.Body>
        {#each rows as row (variableIdentity(row.context, row.name))}
          {@const key = variableIdentity(row.context, row.name)}
          <Table.Row onclick={() => showDetails(row)}>
            <Table.Cell class="min-w-48 font-mono"><button class="text-left hover:underline" onclick={(event) => { event.stopPropagation(); showDetails(row) }}>{row.name}</button></Table.Cell>
            <Table.Cell>{row.context}</Table.Cell>
            <Table.Cell><div class="flex items-center gap-2"><span class="max-w-72 truncate font-mono">{revealed[key] ? row.value : "••••••••"}</span><Button variant="ghost" size="icon" aria-label={`${revealed[key] ? "Hide" : "Show"} value for ${row.name}`} onclick={(event) => { event.stopPropagation(); revealed = { ...revealed, [key]: !revealed[key] } }}>{#if revealed[key]}<EyeOff class="size-4" />{:else}<Eye class="size-4" />{/if}</Button></div></Table.Cell>
            <Table.Cell><div onclick={(event) => event.stopPropagation()} role="presentation">{#key resets[key]}<Switch checked={row.attached} disabled={updating[key]} aria-label={`Inject ${row.name} into workspaces`} onCheckedChange={(checked) => setAttached(row, checked)} />{/key}</div>{#if errors[key]}<p role="alert" class="max-w-60 text-xs text-destructive">{errors[key]}</p>{/if}</Table.Cell>
            <Table.Cell><div onclick={(event) => event.stopPropagation()} role="presentation"><DropdownMenu.Root><DropdownMenu.Trigger>{#snippet child({ props })}<Button {...props} variant="ghost" size="icon" aria-label={`Actions for ${row.name}`}><Ellipsis class="size-4" /></Button>{/snippet}</DropdownMenu.Trigger><DropdownMenu.Content><DropdownMenu.Item onclick={() => showDetails(row)}>View details</DropdownMenu.Item><DropdownMenu.Item onclick={() => showDetails(row)}>Edit value</DropdownMenu.Item><DropdownMenu.Item onclick={() => requestDelete(row)}>Delete</DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Root></div></Table.Cell>
          </Table.Row>
        {/each}
      </Table.Body>
    </Table.Root>
  {/if}
</div>
<AddEnvironmentVariableDialog bind:open={addOpen} />
<EnvironmentVariableDetailsSheet bind:open={detailsOpen} variable={selectedRow} updating={updating[selectedKey]} reset={resets[selectedKey] ?? 0} attachmentError={errors[selectedKey]} onattachment={setAttached} ondelete={requestDelete} />
<ConfirmDialog bind:open={deleteOpen} title="Delete environment variable" description={`Delete '${pendingDelete?.name ?? ""}' from context '${pendingDelete?.context ?? ""}'? ${deleteError}`} confirmLabel="Delete" loading={deleting} onconfirm={remove} />
