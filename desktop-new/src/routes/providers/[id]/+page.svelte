<script lang="ts">
import { page } from "$app/stores"
import { goto } from "$app/navigation"
import { onMount } from "svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { Label } from "$lib/components/ui/label/index.js"
import { Separator } from "$lib/components/ui/separator/index.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import ConfirmDialog from "$lib/components/layout/ConfirmDialog.svelte"
import { providers } from "$lib/stores/providers.js"
import {
  providerUse,
  providerUpdate,
  providerDelete,
  providerOptions,
  providerSetOptions,
} from "$lib/ipc/commands.js"
import { toasts } from "$lib/stores/toasts.js"
import type { ProviderOption } from "$lib/types/index.js"

let id = $derived($page.params.id)
let provider = $derived($providers.find((p) => p.name === id))

let options = $state<Record<string, ProviderOption>>({})
let optionValues = $state<Record<string, string>>({})
let saving = $state(false)
let loading = $state(true)
let confirmDeleteOpen = $state(false)
let deleting = $state(false)

// Group options by their group field, with ungrouped options first
let groupedOptions = $derived.by(() => {
  const groups: Record<string, [string, ProviderOption][]> = {}
  for (const [key, opt] of Object.entries(options)) {
    if (opt.hidden) continue
    const group = opt.group ?? ""
    if (!groups[group]) groups[group] = []
    groups[group].push([key, opt])
  }
  return groups
})

onMount(async () => {
  try {
    const raw = await providerOptions(id)
    options = raw as unknown as Record<string, ProviderOption>

    // Initialize values: prefer current provider option values, then defaults
    const currentOpts = provider?.options ?? {}
    for (const [key, opt] of Object.entries(options)) {
      const currentVal = currentOpts[key]
      if (currentVal?.value != null) {
        optionValues[key] = String(currentVal.value)
      } else if (opt.default != null) {
        optionValues[key] = String(opt.default)
      } else {
        optionValues[key] = ""
      }
    }
  } catch {
    // Options not available
  } finally {
    loading = false
  }
})

async function handleSetDefault() {
  try {
    await providerUse(id)
    toasts.success(`Set ${id} as default provider`)
  } catch (err) {
    toasts.error(`Failed to set default: ${err}`)
  }
}

async function handleUpdate() {
  try {
    await providerUpdate(id)
    toasts.success(`Updated ${id}`)
  } catch (err) {
    toasts.error(`Failed to update: ${err}`)
  }
}

async function handleDelete() {
  deleting = true
  try {
    await providerDelete(id)
    toasts.success(`Deleted ${id}`)
    confirmDeleteOpen = false
    goto("/providers")
  } catch (err) {
    toasts.error(`Failed to delete: ${err}`)
  } finally {
    deleting = false
  }
}

async function handleSaveOptions() {
  saving = true
  try {
    const values: Record<string, string> = {}
    for (const [key, val] of Object.entries(optionValues)) {
      if (val !== "") values[key] = val
    }
    await providerSetOptions(id, values)
    toasts.success("Options saved")
  } catch (err) {
    toasts.error(`Failed to save options: ${err}`)
  } finally {
    saving = false
  }
}
</script>

<div class="space-y-6">
  <div class="flex items-center gap-4">
    <Button variant="ghost" size="sm" onclick={() => goto("/providers")}>
      &larr; Back
    </Button>
    <h1 class="text-2xl font-bold">{id}</h1>
    {#if provider?.version}
      <span class={badgeVariants({ variant: "secondary" })}>{provider.version}</span>
    {/if}
    {#if provider?.state?.initialized}
      <span class={badgeVariants({ variant: "default" })}>initialized</span>
    {/if}
  </div>

  <div class="flex gap-2">
    <Button variant="outline" size="sm" onclick={handleSetDefault}>Set Default</Button>
    <Button variant="outline" size="sm" onclick={handleUpdate}>Update</Button>
    <Button variant="destructive" size="sm" onclick={() => (confirmDeleteOpen = true)}>Delete</Button>
  </div>

  {#if !provider}
    <p class="text-muted-foreground">Provider not found.</p>
  {:else}
    {#if provider.description}
      <p class="text-muted-foreground">{provider.description}</p>
    {/if}

    <Separator />

    <div class="space-y-4">
      <h2 class="text-lg font-semibold">Options</h2>

      {#if loading}
        <p class="text-sm text-muted-foreground">Loading options...</p>
      {:else if Object.keys(options).length === 0}
        <p class="text-sm text-muted-foreground">No configurable options available.</p>
      {:else}
        {#each Object.entries(groupedOptions) as [group, entries] (group)}
          {#if group}
            <h3 class="mt-4 text-sm font-medium text-muted-foreground uppercase tracking-wider">
              {group}
            </h3>
          {/if}

          <div class="space-y-4">
            {#each entries as [key, opt] (key)}
              <div class="space-y-1.5">
                <Label>
                  {opt.displayName ?? opt.name ?? key}
                  {#if opt.required}
                    <span class="text-destructive">*</span>
                  {/if}
                </Label>
                {#if opt.description}
                  <p class="text-xs text-muted-foreground">{opt.description}</p>
                {/if}
                {#if opt.enum && opt.enum.length > 0}
                  <select
                    class="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    value={optionValues[key] ?? ""}
                    onchange={(e) => (optionValues[key] = e.currentTarget.value)}
                  >
                    <option value="">-- Select --</option>
                    {#each opt.enum as enumVal}
                      <option value={enumVal}>{enumVal}</option>
                    {/each}
                  </select>
                {:else}
                  <Input
                    type={opt.password ? "password" : "text"}
                    placeholder={opt.default != null ? String(opt.default) : ""}
                    value={optionValues[key] ?? ""}
                    oninput={(e) => (optionValues[key] = e.currentTarget.value)}
                  />
                {/if}
              </div>
            {/each}
          </div>
        {/each}

        <Button onclick={handleSaveOptions} disabled={saving}>
          {saving ? "Saving..." : "Save Options"}
        </Button>
      {/if}
    </div>
  {/if}
</div>

<ConfirmDialog
  bind:open={confirmDeleteOpen}
  title="Delete provider"
  description="This will remove provider '{id}' and its configuration. Any workspaces using this provider will need a new one."
  confirmLabel="Delete"
  loading={deleting}
  onconfirm={handleDelete}
/>
