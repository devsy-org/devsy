<script lang="ts">
import { onMount } from "svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { Label } from "$lib/components/ui/label/index.js"
import { Separator } from "$lib/components/ui/separator/index.js"
import * as Select from "$lib/components/ui/select/index.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import * as Sheet from "$lib/components/ui/sheet/index.js"
import * as ButtonGroup from "$lib/components/ui/button-group/index.js"
import { Spinner } from "$lib/components/ui/spinner/index.js"
import ConfirmDialog from "$lib/components/layout/ConfirmDialog.svelte"
import {
  providerUse,
  providerUpdate,
  providerDelete,
  providerInit,
  providerList,
  providerOptions,
  providerSetOptions,
  providerRename,
} from "$lib/ipc/commands.js"
import { providers } from "$lib/stores/providers.js"
import { toasts } from "$lib/stores/toasts.js"
import { extractErrorMessage } from "$lib/utils/error.js"
import type { Provider, ProviderOption } from "$lib/types/index.js"

let {
  provider,
  open = $bindable(false),
  ondeleted,
}: {
  provider: Provider
  open: boolean
  ondeleted?: () => void
} = $props()

let options = $state<Record<string, ProviderOption>>({})
let optionValues = $state<Record<string, string>>({})
let initialValues = $state<Record<string, string>>({})
let saving = $state(false)
let loading = $state(true)
let confirmDeleteOpen = $state(false)
let deleting = $state(false)
let renaming = $state(false)
let renameValue = $state("")
let renameSaving = $state(false)
let initializing = $state(false)
let loadedFor = $state<string | null>(null)

let isDirty = $derived.by(() => {
  for (const key of Object.keys(optionValues)) {
    if (optionValues[key] !== (initialValues[key] ?? "")) return true
  }
  return false
})

let requiredOptions = $derived.by(() => {
  return Object.entries(options).filter(
    ([, opt]) => opt.required && !opt.hidden,
  )
})

let hasUnfilledRequired = $derived.by(() => {
  return requiredOptions.some(([key]) => !optionValues[key]?.trim())
})

let groupedOptions = $derived.by(() => {
  const groups: Record<string, [string, ProviderOption][]> = {}
  for (const [key, opt] of Object.entries(options)) {
    if (opt.hidden) continue
    const group = opt.group ?? ""
    if (!groups[group]) groups[group] = []
    groups[group].push([key, opt])
  }
  for (const entries of Object.values(groups)) {
    entries.sort((a, b) => {
      const aReq = a[1].required ? 0 : 1
      const bReq = b[1].required ? 0 : 1
      return aReq - bReq
    })
  }
  return groups
})

async function loadOptions() {
  loading = true
  try {
    const raw = await providerOptions(provider.name)
    options = raw as unknown as Record<string, ProviderOption>

    for (const [key, opt] of Object.entries(options)) {
      if (opt.value != null) {
        optionValues[key] = String(opt.value)
      } else if (opt.default != null) {
        optionValues[key] = String(opt.default)
      } else {
        optionValues[key] = ""
      }
    }
    initialValues = { ...optionValues }
  } catch (err) {
    toasts.error(`Failed to load options: ${extractErrorMessage(err)}`)
  } finally {
    loading = false
  }
}

$effect(() => {
  if (!open) {
    loadedFor = null
    return
  }
  if (loadedFor !== provider.name) {
    loadedFor = provider.name
    loadOptions()
  }
})

async function handleSetDefault() {
  try {
    await providerUse(provider.name)
    toasts.success(`Set ${provider.name} as default provider`)
  } catch (err) {
    toasts.error(`Failed to set default: ${extractErrorMessage(err)}`)
  }
}

async function handleUpdate() {
  try {
    await providerUpdate(provider.name)
    toasts.success(`Updated ${provider.name}`)
  } catch (err) {
    toasts.error(`Failed to update: ${extractErrorMessage(err)}`)
  }
}

async function handleInitialize() {
  initializing = true
  try {
    await providerInit(provider.name)
    const updated = await providerList()
    providers.set(updated)
    toasts.success(`Initialized ${provider.name}`)
  } catch (err) {
    toasts.error(`Failed to initialize: ${extractErrorMessage(err)}`)
  } finally {
    initializing = false
  }
}

async function handleDelete() {
  deleting = true
  try {
    await providerDelete(provider.name)
    toasts.success(`Deleted ${provider.name}`)
    confirmDeleteOpen = false
    open = false
    ondeleted?.()
  } catch (err) {
    toasts.error(`Failed to delete: ${extractErrorMessage(err)}`)
  } finally {
    deleting = false
  }
}

function startRename() {
  renameValue = provider.name
  renaming = true
}

async function handleRename() {
  const trimmed = renameValue.trim()
  if (!trimmed || trimmed === provider.name) {
    renaming = false
    return
  }
  renameSaving = true
  try {
    await providerRename(provider.name, trimmed)
    toasts.success(`Renamed provider to ${trimmed}`)
    renaming = false
    open = false
  } catch (err) {
    toasts.error(`Failed to rename: ${extractErrorMessage(err)}`)
  } finally {
    renameSaving = false
  }
}

async function handleSaveOptions() {
  const missing = requiredOptions
    .filter(([key]) => !optionValues[key]?.trim())
    .map(([, opt]) => opt.displayName ?? opt.name ?? "")
  if (missing.length > 0) {
    toasts.error(`Required: ${missing.join(", ")}`)
    return
  }
  saving = true
  try {
    const values: Record<string, string> = {}
    for (const [key, val] of Object.entries(optionValues)) {
      if (val !== "") values[key] = val
    }
    await providerSetOptions(provider.name, values)
    const updated = await providerList()
    providers.set(updated)
    initialValues = { ...optionValues }
    toasts.success("Options saved")
  } catch (err) {
    toasts.error(`Failed to save options: ${extractErrorMessage(err)}`)
  } finally {
    saving = false
  }
}
</script>

<Sheet.Root bind:open>
  <Sheet.ResizableContent>
    <Sheet.Header class="p-6">
      <Sheet.Title class="flex items-center gap-2">
        {#if renaming}
          <form class="flex items-center gap-2" onsubmit={(e) => { e.preventDefault(); handleRename() }}>
            <Input
              value={renameValue}
              oninput={(e) => (renameValue = e.currentTarget.value)}
              class="h-7 w-48 text-sm"
              disabled={renameSaving}
            />
            <Button variant="outline" size="sm" type="submit" disabled={renameSaving || !renameValue.trim()}>
              {renameSaving ? "Saving..." : "Save"}
            </Button>
            <Button variant="ghost" size="sm" type="button" onclick={() => (renaming = false)} disabled={renameSaving}>
              Cancel
            </Button>
          </form>
        {:else}
          {provider.name}
        {/if}
        {#if provider.version}
          <span class={badgeVariants({ variant: "outline" })}>{provider.version}</span>
        {/if}
        {#if provider.isDefault}
          <span class={badgeVariants({ variant: "default" })}>default</span>
        {/if}
        {#if provider.state?.initialized}
          <span class={badgeVariants({ variant: "secondary" })}>initialized</span>
        {/if}
      </Sheet.Title>
      {#if provider.description}
        <Sheet.Description>{provider.description}</Sheet.Description>
      {/if}
    </Sheet.Header>

    <div class="flex items-center gap-2 px-6">
      {#if provider.state?.initialized !== true}
        <Button variant="outline" size="sm" onclick={handleInitialize} disabled={initializing}>
          {#if initializing}
            <Spinner class="mr-2 size-3" />
          {/if}
          {initializing ? "Initializing..." : "Initialize"}
        </Button>
      {/if}
      <ButtonGroup.Root>
        <Button variant="outline" size="sm" onclick={startRename}>Rename</Button>
        <Button variant="outline" size="sm" onclick={handleSetDefault}>Set Default</Button>
        <Button variant="outline" size="sm" onclick={handleUpdate}>Update</Button>
      </ButtonGroup.Root>
      <Button variant="destructive" size="sm" onclick={() => (confirmDeleteOpen = true)}>Delete</Button>
    </div>

    <Separator />

    <div class="flex-1 overflow-y-auto space-y-4 px-6 pb-6">
      {#if loading}
        <p class="text-sm text-muted-foreground">Loading options...</p>
      {:else if Object.keys(options).length === 0}
        <p class="text-sm text-muted-foreground">No configurable options available.</p>
      {:else}
        {#each Object.entries(groupedOptions) as [group, entries] (group)}
          {#if group}
            <h3 class="text-xs font-medium text-muted-foreground uppercase tracking-wider pt-2">
              {group}
            </h3>
          {/if}

          {#each entries as [key, opt] (key)}
            <div class="space-y-1.5">
              <Label class="text-sm">
                {opt.displayName ?? opt.name ?? key}
                {#if opt.required}
                  <span class="text-destructive">*</span>
                {/if}
              </Label>
              {#if opt.description}
                <p class="text-xs text-muted-foreground">{opt.description}</p>
              {/if}
              {#if opt.enum && opt.enum.length > 0}
                <Select.Root
                  type="single"
                  value={optionValues[key] ?? ""}
                  onValueChange={(v) => (optionValues[key] = v)}
                >
                  <Select.Trigger class="w-full h-9">
                    <span>{optionValues[key] || "-- Select --"}</span>
                  </Select.Trigger>
                  <Select.Content>
                    {#each opt.enum as enumVal}
                      <Select.Item value={enumVal} label={enumVal} />
                    {/each}
                  </Select.Content>
                </Select.Root>
              {:else}
                <Input
                  type={opt.password ? "password" : "text"}
                  placeholder={opt.default != null ? String(opt.default) : ""}
                  value={optionValues[key] ?? ""}
                  oninput={(e) => (optionValues[key] = e.currentTarget.value)}
                  class="h-9"
                />
              {/if}
            </div>
          {/each}
        {/each}
      {/if}
    </div>

    {#if isDirty}
      <Sheet.Footer class="p-6">
        <div class="flex items-center gap-2">
          <Button onclick={handleSaveOptions} disabled={saving} size="sm">
            {saving ? "Saving..." : "Save"}
          </Button>
          <Button variant="outline" size="sm" onclick={() => { optionValues = { ...initialValues } }}>
            Reset
          </Button>
          <span class="text-xs text-muted-foreground">Unsaved changes</span>
        </div>
      </Sheet.Footer>
    {/if}
  </Sheet.ResizableContent>
</Sheet.Root>

<ConfirmDialog
  bind:open={confirmDeleteOpen}
  title="Delete provider"
  description="This will remove provider '{provider.name}' and its configuration. Any workspaces using this provider will need a new one."
  confirmLabel="Delete"
  loading={deleting}
  onconfirm={handleDelete}
/>
