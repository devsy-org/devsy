<script lang="ts">
import { goto, querystring } from "$lib/router.js"
import { onMount } from "svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { Label } from "$lib/components/ui/label/index.js"
import { Separator } from "$lib/components/ui/separator/index.js"
import * as Select from "$lib/components/ui/select/index.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import * as Alert from "$lib/components/ui/alert/index.js"
import { TriangleAlert, Star } from "@lucide/svelte"
import ConfirmDialog from "$lib/components/layout/ConfirmDialog.svelte"
import UpdateConfirmDialog from "$lib/components/provider/UpdateConfirmDialog.svelte"
import ProviderIcon from "$lib/components/provider/ProviderIcon.svelte"
import { providers } from "$lib/stores/providers.js"
import {
  providerVersions,
  loadVersionsFor,
  refreshUpdates,
} from "$lib/stores/providerVersions.js"
import {
  providerInit,
  providerUse,
  providerUpdate,
  providerDelete,
  providerOptions,
  providerSetOptions,
  providerSetVersion,
} from "$lib/ipc/commands.js"
import { toasts } from "$lib/stores/toasts.js"
import { Skeleton } from "$lib/components/ui/skeleton/index.js"
import { extractErrorMessage } from "$lib/utils/error.js"
import type { ProviderOption } from "$lib/types/index.js"

let { params = {} }: { params?: Record<string, string> } = $props()

let id = $derived(params.id ?? "")
let provider = $derived($providers.find((p) => p.name === id))
let isSetup = $derived.by(() => {
  const qs = new URLSearchParams($querystring ?? "")
  return qs.get("setup") === "true"
})
let isInitialized = $derived(provider?.state?.initialized === true)

let options = $state<Record<string, ProviderOption>>({})
let optionValues = $state<Record<string, string>>({})
let initialValues = $state<Record<string, string>>({})
let saving = $state(false)
let loading = $state(true)
let confirmDeleteOpen = $state(false)
let deleting = $state(false)
let initializing = $state(false)
let confirmUpdateOpen = $state(false)
let updating = $state(false)
let confirmSwitchOpen = $state(false)
let targetTag = $state("")
let switching = $state(false)

function openVersionSwitch(tag: string) {
  targetTag = tag
  confirmSwitchOpen = true
}

let deleteDescription = $derived.by(() => {
  const others = $providers.filter((p) => p.name !== id && p.state?.initialized)
  if (provider?.isDefault && others.length > 0) {
    return `Deleting '${id}' will leave no default provider. Pick a new default from the list after deletion, or use the \`--provider\` flag on CLI commands.`
  }
  return `This will remove provider '${id}' and its configuration. Any workspaces using this provider will need a new one.`
})

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

// Group options by their group field, with ungrouped options first
// In setup mode, show required options first regardless of group
let groupedOptions = $derived.by(() => {
  const groups: Record<string, [string, ProviderOption][]> = {}
  for (const [key, opt] of Object.entries(options)) {
    if (opt.hidden) continue
    const group = opt.group ?? ""
    if (!groups[group]) groups[group] = []
    groups[group].push([key, opt])
  }
  // Sort required options first within each group
  for (const entries of Object.values(groups)) {
    entries.sort((a, b) => {
      const aReq = a[1].required ? 0 : 1
      const bReq = b[1].required ? 0 : 1
      return aReq - bReq
    })
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
    initialValues = { ...optionValues }
  } catch {
    // Options not available
  } finally {
    loading = false
  }
  loadVersionsFor(id).catch(() => {})
})

async function handleSetDefault() {
  try {
    await providerUse(id)
    toasts.success(`Set ${id} as default provider`)
  } catch (err) {
    toasts.error(`Failed to set default: ${extractErrorMessage(err)}`)
  }
}

async function handleInitialize() {
  initializing = true
  try {
    await providerInit(id)
    toasts.success(`Initialized ${id}`)
  } catch (err) {
    toasts.error(`Failed to initialize: ${extractErrorMessage(err)}`)
  } finally {
    initializing = false
  }
}

function handleUpdate() {
  confirmUpdateOpen = true
}

async function runUpdate() {
  updating = true
  try {
    await providerUpdate(id)
    toasts.success(`Updated ${id}`)
    await loadVersionsFor(id)
    await refreshUpdates()
  } catch (err) {
    toasts.error(`Failed to update: ${extractErrorMessage(err)}`)
  } finally {
    updating = false
    confirmUpdateOpen = false
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
    toasts.error(`Failed to delete: ${extractErrorMessage(err)}`)
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
    initialValues = { ...optionValues }
    if (isSetup) {
      toasts.success(`Provider ${id} configured successfully`)
      goto("/providers")
    } else {
      toasts.success("Options saved")
    }
  } catch (err) {
    toasts.error(`Failed to save options: ${extractErrorMessage(err)}`)
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
    <ProviderIcon name={id} class="size-8" />
    <h1 class="text-2xl font-bold">{id}</h1>
    {#if $providerVersions.byProvider[id] && !$providerVersions.byProvider[id].unsupported && ($providerVersions.byProvider[id].versions?.length ?? 0) > 0}
      {@const entry = $providerVersions.byProvider[id]}
      {@const currentTag = provider?.version ?? entry.versions.find((v) => v.current)?.tag ?? ""}
      <Select.Root
        type="single"
        value={currentTag}
        onValueChange={(v) => {
          if (v && v !== currentTag) openVersionSwitch(v)
        }}
      >
        <Select.Trigger class="w-[160px] h-7">
          <span>{currentTag || "Select version"}</span>
        </Select.Trigger>
        <Select.Content>
          {#each entry.versions as v (v.tag)}
            <Select.Item value={v.tag} label={v.tag} />
          {/each}
        </Select.Content>
      </Select.Root>
    {:else if provider?.version}
      <span class={badgeVariants({ variant: "secondary" })}>{provider.version}</span>
    {/if}
    {#if provider?.state?.initialized}
      <span class={badgeVariants({ variant: "default" })}>initialized</span>
    {/if}
    {#if provider?.isDefault}
      <span class="{badgeVariants({ variant: 'default' })} gap-1">
        <Star class="size-3" />
        Default
      </span>
    {/if}
  </div>

  {#if provider}
    {#if $providerVersions.updates[id]?.updateAvailable === true}
      <Alert.Root class="border-amber-500/50 bg-amber-500/10">
        <Alert.Title>Update available: {$providerVersions.updates[id].latest}</Alert.Title>
        <Alert.Description>
          <Button size="sm" class="mt-2" onclick={handleUpdate}>
            Install update
          </Button>
        </Alert.Description>
      </Alert.Root>
    {/if}
    <div class="flex gap-2">
      {#if !isInitialized}
        <Button size="sm" onclick={handleInitialize} disabled={initializing}>
          {initializing ? 'Initializing...' : 'Initialize'}
        </Button>
      {:else if !provider?.isDefault}
        <Button variant="outline" size="sm" onclick={handleSetDefault}>Set Default</Button>
      {/if}
      <Button variant="outline" size="sm" onclick={handleUpdate}>Update</Button>
      <Button variant="outline" size="sm" onclick={async () => { await refreshUpdates(); await loadVersionsFor(id) }}>Check for updates</Button>
      <Button variant="destructive" size="sm" onclick={() => (confirmDeleteOpen = true)}>Delete</Button>
    </div>
  {/if}

  {#if !provider}
    <p class="text-muted-foreground">Provider not found.</p>
  {:else}
    {#if isSetup && !loading && hasUnfilledRequired}
      <div class="rounded-md border border-amber-500/50 bg-amber-500/10 p-4">
        <h3 class="font-semibold text-amber-700 dark:text-amber-400">Configure required options</h3>
        <p class="mt-1 text-sm text-amber-600 dark:text-amber-400/80">
          This provider needs configuration before it can be used.
          Fill in the required fields below and save.
        </p>
      </div>
    {:else if !isInitialized && !loading}
      <Alert.Root class="border-amber-500/50 bg-amber-500/10">
        <TriangleAlert class="text-amber-600" />
        <Alert.Title>Provider not initialized</Alert.Title>
        <Alert.Description>
          This provider needs to be initialized before it can create workspaces.
          <Button size="sm" class="mt-2" onclick={handleInitialize} disabled={initializing}>
            {initializing ? 'Initializing...' : 'Initialize Provider'}
          </Button>
        </Alert.Description>
      </Alert.Root>
    {/if}

    {#if provider.description}
      <p class="text-muted-foreground">{provider.description}</p>
    {/if}

    <Separator />

    <div class="space-y-4">
      <h2 class="text-lg font-semibold">
        {isSetup ? "Configure Provider" : "Options"}
      </h2>

      {#if loading}
        <div class="space-y-4">
          {#each { length: 4 } as _}
            <div class="space-y-1.5">
              <Skeleton class="h-4 w-32" />
              <Skeleton class="h-9 w-full" />
            </div>
          {/each}
          <Skeleton class="h-9 w-20" />
        </div>
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
              <div class="space-y-1.5 {isSetup && opt.required && !optionValues[key]?.trim() ? 'rounded-md border border-amber-500/30 bg-amber-500/5 p-3 -mx-3' : ''}"  >
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
                  <Select.Root
                    type="single"
                    value={optionValues[key] ?? ""}
                    onValueChange={(v) => (optionValues[key] = v)}
                  >
                    <Select.Trigger class="w-full">
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
                  />
                {/if}
              </div>
            {/each}
          </div>
        {/each}

        <div class="flex items-center gap-3">
          <Button onclick={handleSaveOptions} disabled={saving || !isDirty}>
            {saving ? "Saving..." : "Save"}
          </Button>
          {#if isSetup && !hasUnfilledRequired}
            <Button variant="outline" onclick={() => goto("/providers")}>
              Skip
            </Button>
          {:else if isDirty}
            <Button variant="outline" onclick={() => { optionValues = { ...initialValues } }}>
              Reset
            </Button>
            <span class="text-xs text-muted-foreground">Unsaved changes</span>
          {/if}
        </div>
      {/if}
    </div>
  {/if}
</div>

<ConfirmDialog
  bind:open={confirmDeleteOpen}
  title="Delete provider"
  description={deleteDescription}
  confirmLabel="Delete"
  loading={deleting}
  onconfirm={handleDelete}
/>

<UpdateConfirmDialog
  bind:open={confirmUpdateOpen}
  providerName={id}
  currentVersion={provider?.version}
  latestVersion={$providerVersions.updates[id]?.latest}
  loading={updating}
  onconfirm={runUpdate}
/>

<ConfirmDialog
  bind:open={confirmSwitchOpen}
  title={`Switch '${id}' from ${provider?.version ?? ""} to ${targetTag}`}
  description={`Workspaces created with ${provider?.version ?? ""} may behave differently after this change. Existing workspaces will run against ${targetTag} the next time they're used.`}
  confirmLabel="Switch"
  loading={switching}
  onconfirm={async () => {
    switching = true
    try {
      await providerSetVersion(id, targetTag)
      toasts.success(`Switched ${id} to ${targetTag}`)
      await loadVersionsFor(id)
      await refreshUpdates()
    } catch (err) {
      toasts.error(`Failed to switch version: ${extractErrorMessage(err)}`)
    } finally {
      switching = false
      confirmSwitchOpen = false
    }
  }}
/>
