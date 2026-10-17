<script lang="ts">
import { onDestroy, onMount } from "svelte"
import { Star } from "@lucide/svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { Label } from "$lib/components/ui/label/index.js"
import { Switch } from "$lib/components/ui/switch/index.js"
import * as Select from "$lib/components/ui/select/index.js"
import * as Alert from "$lib/components/ui/alert/index.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import * as Sheet from "$lib/components/ui/sheet/index.js"
import * as ButtonGroup from "$lib/components/ui/button-group/index.js"
import { Spinner } from "$lib/components/ui/spinner/index.js"
import ConfirmDialog from "$lib/components/layout/ConfirmDialog.svelte"
import ErrorCard from "$lib/components/ErrorCard.svelte"
import LogTable from "$lib/components/log/LogTable.svelte"
import UpdateConfirmDialog from "./UpdateConfirmDialog.svelte"
import type { CLIError } from "$shared/cli-error.js"
import {
  providerUse,
  providerUpdateStreaming,
  providerDelete,
  providerInit,
  providerList,
  providerOptions,
  providerRefreshState,
  providerSetOptions,
  providerSetSingleMachine,
  providerRename,
  providerSetVersion,
} from "$lib/ipc/commands.js"
import { onCommandProgress } from "$lib/ipc/events.js"
import { providers, providerJobs } from "$lib/stores/providers.js"
import {
  providerVersions,
  loadVersionsFor,
  refreshUpdates,
} from "$lib/stores/providerVersions.js"
import { toasts } from "$lib/stores/toasts.js"
import { extractErrorMessage } from "$lib/utils/error.js"
import {
  initialProviderOptionValue,
  normalizeProviderOptionEnums,
  providerOptionEnumHasValue,
} from "$lib/utils/provider-option-enum.js"
import { isCommandSuccess } from "$lib/utils/log-parser.js"
import { providerSourceReference } from "$lib/utils/provider-source.js"
import { providerStatus } from "$lib/utils/provider-status.js"
import type {
  CommandProgress,
  Provider,
  ProviderOption,
} from "$lib/types/index.js"

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
let optionsError = $state("")
let optionSearch = $state("")
let optionsRequest = 0
let confirmDeleteOpen = $state(false)
let deleting = $state(false)
let renaming = $state(false)
let renameValue = $state("")
let renameSaving = $state(false)
let initializing = $state(false)
let initError = $state<CLIError | null>(null)
let loadedFor = $state<string | null>(null)
let confirmUpdateOpen = $state(false)
let updating = $state(false)
let updateCommandId = $state<string | null>(null)
let updateProviderName = $state<string | null>(null)
let updateLines = $state<string[]>([])
let updateError = $state<CLIError | null>(null)
let updateStartError = $state("")
let recoveredUpdateState = $state(false)
let unlistenProgress: (() => void) | undefined
let pendingUpdateProgress: CommandProgress[] = []
let confirmSwitchOpen = $state(false)
let targetTag = $state("")
let switching = $state(false)
let singleMachineOverride = $state<boolean | undefined>(undefined)
let singleMachineOverrideProviderName = $state<string | null>(null)
let togglingSingleMachine = $state(false)
let effectiveSingleMachine = $derived(
  singleMachineOverrideProviderName === provider.name &&
    singleMachineOverride !== undefined
    ? singleMachineOverride
    : provider.state?.singleMachine,
)
let providerJob = $derived(provider ? $providerJobs[provider.name] : undefined)
let status = $derived(
  provider
    ? providerStatus(provider, providerJob)
    : { kind: "uninitialized" as const, label: "" },
)
let persistedUpdateError = $derived.by((): CLIError | null => {
  if (
    recoveredUpdateState ||
    providerJob?.activity !== "updating" ||
    !providerJob.error
  )
    return null
  return {
    code: providerJob.errorCode ?? "provider_update_failed",
    message: providerJob.error,
    hint: providerJob.errorHint,
    context: providerJob.errorContext,
  }
})
let visibleUpdateError = $derived(updateError ?? persistedUpdateError)
let refreshRecovery = $derived(
  visibleUpdateError?.code === "provider_refresh_failed",
)
let visibleUpdateLines = $derived(
  updateLines.length > 0 ? updateLines : (providerJob?.logs ?? []),
)
let refreshingProviderState = $state(false)
let showingLocalUpdate = $derived(updateProviderName === provider.name)

let updatePhase = $derived.by(() => {
  if (!updating || !showingLocalUpdate) return ""
  const phase = providerJob?.phase ?? ""
  if (phase.includes("init")) return "Initializing provider"
  return "Downloading provider"
})

function handleUpdateProgress(progress: CommandProgress) {
  const targetName = updateProviderName
  if (progress.message && targetName === provider.name) {
    updateLines = [...updateLines, progress.message]
  }
  if (!progress.done) return
  updating = false
  if (isCommandSuccess(progress.success) && targetName) {
    void finishUpdate(targetName).catch((error) => {
      if (targetName !== provider.name) return
      updateError = {
        code: "provider_refresh_failed",
        message: `The update completed, but DevSy could not refresh ${targetName}: ${extractErrorMessage(error)}`,
      }
    })
  } else if (targetName === provider.name) {
    updateError = progress.cliError ?? {
      code: "provider_update_failed",
      message: "Provider update failed.",
    }
  }
}

onMount(async () => {
  unlistenProgress = await onCommandProgress((progress) => {
    if (updating && !updateCommandId) {
      pendingUpdateProgress = [...pendingUpdateProgress, progress]
      return
    }
    if (progress.commandId === updateCommandId) handleUpdateProgress(progress)
  })
})

onDestroy(() => {
  unlistenProgress?.()
})

function openVersionSwitch(tag: string) {
  targetTag = tag
  confirmSwitchOpen = true
}

let isDirty = $derived.by(() => {
  const keys = new Set([
    ...Object.keys(optionValues),
    ...Object.keys(initialValues),
  ])
  for (const key of keys) {
    const hasCurrentValue = Object.hasOwn(optionValues, key)
    const hadInitialValue = Object.hasOwn(initialValues, key)
    if (hasCurrentValue !== hadInitialValue) return true
    if (optionValues[key] !== initialValues[key]) return true
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

let versionEntry = $derived(
  provider ? $providerVersions.byProvider[provider.name] : undefined,
)

let visibleOptions = $derived(
  Object.entries(options).filter(([, opt]) => !opt.hidden),
)
let matchingOptions = $derived.by(() => {
  const query = optionSearch.trim().toLowerCase()
  return visibleOptions.filter(
    ([key, opt]) =>
      !query ||
      [key, opt.displayName, opt.description].some((text) =>
        text?.toLowerCase().includes(query),
      ),
  )
})
let groupedOptions = $derived.by(() => {
  const groups: Record<string, [string, ProviderOption][]> = {}
  for (const [key, opt] of matchingOptions) {
    const group =
      provider.optionGroups?.find((item) => item.options?.includes(key))
        ?.name ||
      opt.group ||
      "General settings"
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
  const name = provider.name
  const request = ++optionsRequest
  loading = true
  optionsError = ""
  options = {}
  optionValues = {}
  initialValues = {}
  try {
    const raw = await providerOptions(name)
    if (request !== optionsRequest || provider.name !== name || !open) return
    options = raw as unknown as Record<string, ProviderOption>

    const values: Record<string, string> = {}
    for (const [key, opt] of Object.entries(options)) {
      const value = initialProviderOptionValue(opt)
      if (value !== undefined) values[key] = value
    }
    optionValues = values
    initialValues = { ...values }
  } catch (err) {
    if (request !== optionsRequest || provider.name !== name || !open) return
    optionsError = `Failed to load options: ${extractErrorMessage(err)}`
  } finally {
    if (request === optionsRequest && provider.name === name && open)
      loading = false
  }
}

$effect(() => {
  if (
    singleMachineOverride !== undefined &&
    (singleMachineOverrideProviderName !== provider.name ||
      (!togglingSingleMachine &&
        provider.state?.singleMachine === singleMachineOverride))
  ) {
    singleMachineOverride = undefined
    singleMachineOverrideProviderName = null
  }
})

$effect(() => {
  if (!open || !provider) {
    loadedFor = null
    return
  }
  if (loadedFor !== provider.name) {
    loadedFor = provider.name
    optionSearch = ""
    loadOptions()
    loadVersionsFor(provider.name).catch(() => {})
  }
})

async function handleSetDefault() {
  const name = provider.name
  try {
    await providerUse(name)
    toasts.success(`Set ${name} as default provider`)
  } catch (err) {
    toasts.error(`Failed to set default: ${extractErrorMessage(err)}`)
  }
}

async function handleToggleSingleMachine(enabled: boolean) {
  if (saving || togglingSingleMachine) return
  const name = provider.name
  const previousOverride = singleMachineOverride
  const previousOverrideProviderName = singleMachineOverrideProviderName
  togglingSingleMachine = true
  singleMachineOverride = enabled
  singleMachineOverrideProviderName = name
  try {
    await providerSetSingleMachine(name, enabled)
  } catch (err) {
    if (provider.name === name && singleMachineOverrideProviderName === name) {
      singleMachineOverride = previousOverride
      singleMachineOverrideProviderName = previousOverrideProviderName
    }
    toasts.error(`Failed to update reuse machine: ${extractErrorMessage(err)}`)
    togglingSingleMachine = false
    return
  }

  providers.update((current) =>
    current.map((item) =>
      item.name === name
        ? { ...item, state: { ...item.state, singleMachine: enabled } }
        : item,
    ),
  )

  try {
    providers.set(await providerList())
    toasts.success(enabled ? "Reuse machine enabled" : "Reuse machine disabled")
  } catch (err) {
    toasts.error(
      `Reuse machine was updated, but provider state could not be refreshed: ${extractErrorMessage(err)}`,
    )
  } finally {
    togglingSingleMachine = false
  }
}

function handleUpdate() {
  confirmUpdateOpen = true
}

async function runUpdate() {
  const name = provider.name
  updating = true
  updateProviderName = name
  updateCommandId = null
  pendingUpdateProgress = []
  updateError = null
  updateStartError = ""
  recoveredUpdateState = false
  updateLines = []
  confirmUpdateOpen = false
  try {
    const latestVersion = $providerVersions.updates[name]?.latest
    const source = provider.source
    const isPinnedGithub =
      !!source?.github &&
      (source.github.includes("@") || source.raw?.includes("@"))
    if (isPinnedGithub && latestVersion) {
      updateCommandId = await providerUpdateStreaming(name, latestVersion)
    } else {
      updateCommandId = await providerUpdateStreaming(name)
    }
    const matching = pendingUpdateProgress.filter(
      (progress) => progress.commandId === updateCommandId,
    )
    pendingUpdateProgress = []
    for (const progress of matching) handleUpdateProgress(progress)
  } catch (err) {
    updating = false
    updateStartError = `Failed to start update: ${extractErrorMessage(err)}`
  }
}

async function synchronizeProviderAfterUpdate(name: string) {
  providers.set(await providerList())
  await loadVersionsFor(name)
  await refreshUpdates()
}

async function retryProviderStateRefresh() {
  const name = provider.name
  refreshingProviderState = true
  updateStartError = ""
  try {
    await providerRefreshState(name)
    await synchronizeProviderAfterUpdate(name)
    updateError = null
    recoveredUpdateState = true
  } catch (error) {
    updateError = {
      code: "provider_refresh_failed",
      message: `DevSy could not refresh ${name}: ${extractErrorMessage(error)}`,
    }
  } finally {
    refreshingProviderState = false
  }
}

async function finishUpdate(name: string) {
  await synchronizeProviderAfterUpdate(name)
  toasts.success(`Updated ${name}`)
}

async function runSwitch() {
  const name = provider.name
  switching = true
  try {
    await providerSetVersion(name, targetTag)
    toasts.success(`Switched ${name} to ${targetTag}`)
    providers.set(await providerList())
    await loadVersionsFor(name)
    await refreshUpdates()
  } catch (err) {
    toasts.error(`Failed to switch version: ${extractErrorMessage(err)}`)
  } finally {
    switching = false
    confirmSwitchOpen = false
  }
}

/**
 * Pull a structured CLIError off a thrown IPC error if the main process
 * attached one. Electron's structured-clone IPC preserves additional own
 * properties on Error objects, so a `cliError` field set in cli.ts/wrapError
 * survives the bridge.
 */
function extractCliError(err: unknown): CLIError | null {
  if (err && typeof err === "object" && "cliError" in err) {
    const candidate = (err as { cliError?: unknown }).cliError
    if (
      candidate &&
      typeof candidate === "object" &&
      "code" in candidate &&
      "message" in candidate
    ) {
      return candidate as CLIError
    }
  }
  return null
}

async function handleInitialize() {
  const name = provider.name
  initializing = true
  initError = null
  try {
    await providerInit(name)
    const updated = await providerList()
    providers.set(updated)
    toasts.success(`Initialized ${name}`)
  } catch (err) {
    const cliError = extractCliError(err)
    if (cliError) {
      initError = cliError
    } else {
      initError = {
        code: "UNKNOWN",
        message:
          err instanceof Error ? err.message : `Failed to initialize ${name}.`,
      }
    }
  } finally {
    initializing = false
  }
}

async function handleDelete() {
  const name = provider.name
  deleting = true
  try {
    await providerDelete(name)
    toasts.success(`Deleted ${name}`)
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
  if (saving || togglingSingleMachine) return
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
      if (
        !options[key]?.hidden &&
        (val !== "" || providerOptionEnumHasValue(options[key]?.enum, "")) &&
        (!Object.hasOwn(initialValues, key) || val !== initialValues[key])
      ) {
        values[key] = val
      }
    }
    await providerSetOptions(provider.name, values, effectiveSingleMachine)
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

{#if provider}
<Sheet.Root bind:open>
  <Sheet.ResizableContent>
    <Sheet.Header class="shrink-0 border-b p-6 pb-4">
      <Sheet.Title class="flex flex-wrap items-center gap-2 pr-7 [overflow-wrap:anywhere]">
        {#if renaming}
          <form class="flex items-center gap-2" onsubmit={(e) => { e.preventDefault(); handleRename() }}>
            <Input
              aria-label="Provider name"
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
        {#if versionEntry && !versionEntry.unsupported && (versionEntry.versions?.length ?? 0) > 0}
          {@const currentTag = provider.version ?? versionEntry.versions.find((v) => v.current)?.tag ?? ""}
          <Select.Root
            type="single"
            value={currentTag}
            onValueChange={(v) => {
              if (v && v !== currentTag) openVersionSwitch(v)
            }}
          >
            <Select.Trigger aria-label="Provider version" class="w-[140px] h-7">
              <span>{currentTag || "Select version"}</span>
            </Select.Trigger>
            <Select.Content>
              {#each versionEntry.versions as v (v.tag)}
                <Select.Item value={v.tag} label={v.tag} />
              {/each}
            </Select.Content>
          </Select.Root>
        {:else if provider.version}
          <span class={badgeVariants({ variant: "outline" })}>{provider.version}</span>
        {/if}
        {#if provider.isDefault}
          <span class="{badgeVariants({ variant: 'default' })} gap-1">
            <Star class="size-3" />
            Default
          </span>
        {/if}
        {#if status.kind === "ready"}
          <span class={badgeVariants({ variant: "secondary" })}>{status.label}</span>
        {:else if status.kind === "busy"}
          <span class="{badgeVariants({ variant: 'outline' })} gap-1">
            <Spinner class="size-3" />
            {status.label}
          </span>
        {:else if status.kind === "failed"}
          <span
            class={badgeVariants({ variant: "destructive" })}
            title={[status.error, status.errorCode, status.errorHint].filter(Boolean).join(" - ")}
          >
            {status.label}
          </span>
        {:else}
          <span class={badgeVariants({ variant: "destructive" })}>{status.label}</span>
        {/if}
      </Sheet.Title>
      {#if provider.description}
        <Sheet.Description>{provider.description}</Sheet.Description>
      {/if}
      {#if provider.deprecated}
        <Alert.Root class="border-amber-500/50 bg-amber-500/10">
          <Alert.Title>Deprecated built-in provider</Alert.Title>
          <Alert.Description>
            This built-in provider is scheduled for removal. Existing provider records remain available
            during the compatibility period.
          </Alert.Description>
        </Alert.Root>
      {/if}
    </Sheet.Header>

    <div class="min-h-0 flex-1 space-y-5 overflow-y-auto px-6 pb-6">
    <section aria-label="Provider actions" class="flex flex-wrap items-center gap-2">
      {#if status.kind !== "ready" && status.kind !== "busy"}
        <Button variant="outline" size="sm" onclick={handleInitialize} disabled={initializing}>
          {#if initializing}
            <Spinner class="mr-2 size-3" />
          {/if}
          {initializing ? "Initializing..." : "Initialize"}
        </Button>
      {/if}
      <ButtonGroup.Root>
        <Button variant="outline" size="sm" onclick={startRename}>Rename</Button>
        {#if !provider.isDefault}
          <Button variant="outline" size="sm" onclick={handleSetDefault}>Set Default</Button>
        {/if}
        <Button variant="outline" size="sm" onclick={handleUpdate}>Update</Button>
      </ButtonGroup.Root>
      <Button variant="destructive" size="sm" onclick={() => (confirmDeleteOpen = true)}>Delete</Button>
    </section>

    <section aria-labelledby="provider-source-title" class="rounded-lg border p-4">
      <h3 id="provider-source-title" class="mb-2 text-sm font-semibold">Installation source</h3>
      <p class="select-text font-mono text-xs leading-6 [overflow-wrap:anywhere]">{providerSourceReference(provider.source) ?? "Source not available"}</p>
      <p class="mt-1 text-xs leading-5 text-muted-foreground">New workspaces use this provider's current configuration.</p>
    </section>

    <section aria-labelledby="provider-machine-title" class="flex items-start justify-between gap-4 rounded-lg border p-4">
      <div class="space-y-0.5">
        <Label for="provider-reuse-machine" id="provider-machine-title">Reuse machine</Label>
        <p class="text-sm text-muted-foreground">
          Run all workspaces on a single shared machine instead of one per workspace.
        </p>
      </div>
      <Switch
        id="provider-reuse-machine"
        checked={effectiveSingleMachine ?? false}
        onCheckedChange={handleToggleSingleMachine}
        disabled={togglingSingleMachine || saving}
      />
    </section>

    {#if $providerVersions.updates[provider.name]?.updateAvailable === true}
      <div class="pb-2">
        <Alert.Root class="border-amber-500/50 bg-amber-500/10">
          <Alert.Title>Update available: {$providerVersions.updates[provider.name].latest}</Alert.Title>
          <Alert.Description>
            <Button size="sm" class="mt-2" onclick={handleUpdate}>
              Install update
            </Button>
          </Alert.Description>
        </Alert.Root>
      </div>
    {/if}

    {#if initError}
      <div class="pt-4">
        <ErrorCard cliError={initError} />
      </div>
    {/if}

    {#if (showingLocalUpdate && (updating || updateError || updateStartError || updateLines.length > 0)) || persistedUpdateError || providerJob?.logs?.length}
      <div class="space-y-3 pt-4">
        <div
          class="min-h-12 rounded-md border bg-muted/20 px-3 py-2"
          role="status"
          aria-live="polite"
          aria-busy={updating}
        >
          <div class="flex items-center gap-2 text-sm font-semibold">
            {#if updating}<Spinner class="size-4" aria-hidden="true" />{/if}
            {updating ? "Updating" : refreshRecovery ? "Status may be out of date" : visibleUpdateError || updateStartError ? "Update failed" : "Updated"}
          </div>
          <p class="text-sm text-muted-foreground">
            {#if updating}
              {updatePhase}
            {:else if refreshRecovery}
              The update completed, but DevSy could not refresh the provider's current state.
            {:else if visibleUpdateError || updateStartError}
              The update did not complete. Review the error and try again.
            {:else}
              {provider.name} is ready to use.
            {/if}
          </p>
        </div>
        {#if visibleUpdateError}
          <ErrorCard cliError={visibleUpdateError} />
        {:else if updateStartError}
          <Alert.Root variant="destructive"><Alert.Description>{updateStartError}</Alert.Description></Alert.Root>
        {/if}
        {#if visibleUpdateLines.length > 0}
          <details open={updating} class="rounded-md border bg-muted/30">
            <summary class="cursor-pointer select-none px-3 py-2 text-sm font-medium text-muted-foreground hover:text-foreground">
              {updating ? "Update logs" : "Show logs"}
            </summary>
            <LogTable lines={visibleUpdateLines} maxHeightClass="max-h-48" follow={updating} class="border-x-0 border-b-0 rounded-none" />
          </details>
        {/if}
        {#if refreshRecovery}
          <Button variant="outline" size="sm" onclick={retryProviderStateRefresh} disabled={refreshingProviderState}>
            {refreshingProviderState ? "Refreshing..." : "Refresh status"}
          </Button>
        {:else if visibleUpdateError || updateStartError}
          <Button variant="outline" size="sm" onclick={runUpdate}>Retry update</Button>
        {/if}
      </div>
    {/if}

    <section aria-labelledby="provider-options-title" class="space-y-4 border-t pt-5">
      <div class="space-y-1">
        <h3 id="provider-options-title" class="text-sm font-semibold">Provider options</h3>
        <p class="text-xs leading-5 text-muted-foreground">Review settings supplied by this provider. Required fields are marked with an asterisk.</p>
      </div>

    <div class="space-y-4">
      {#if !loading && !optionsError && visibleOptions.length > 0}
        <Input aria-label="Search provider options" placeholder="Search options by name or description" value={optionSearch} oninput={(event) => (optionSearch = event.currentTarget.value)} />
        <p class="text-xs text-muted-foreground" aria-live="polite">{matchingOptions.length} of {visibleOptions.length} options</p>
      {/if}
      {#if loading}
        <p class="text-sm text-muted-foreground">Loading options...</p>
      {:else if optionsError}
        <Alert.Root variant="destructive"><Alert.Description>{optionsError}</Alert.Description></Alert.Root>
        <Button variant="outline" size="sm" onclick={loadOptions}>Retry options</Button>
      {:else if visibleOptions.length === 0}
        <p class="text-sm text-muted-foreground">No configurable options available.</p>
      {:else if matchingOptions.length === 0}
        <p class="text-sm text-muted-foreground">No options match your search.</p>
        <Button variant="outline" size="sm" onclick={() => (optionSearch = "")}>Clear option search</Button>
      {:else}
        {#each Object.entries(groupedOptions) as [group, entries] (group)}
          {#if group}
            <h4 class="border-b pb-2 pt-2 text-sm font-medium">
              {group}
            </h4>
          {/if}

          {#each entries as [key, opt] (key)}
            <div class="space-y-1.5">
              <Label for={`provider-option-${key}`} class="text-sm">
                {opt.displayName ?? opt.name ?? key}
                {#if opt.required}
                  <span class="text-destructive">*</span>
                {/if}
              </Label>
              {#if opt.description}
                <p id={`provider-option-help-${key}`} class="text-xs leading-5 text-muted-foreground">{opt.description}</p>
              {/if}
              {#if opt.enum && opt.enum.length > 0}
                {@const enumOptions = normalizeProviderOptionEnums(opt.enum)}
                {@const selectedOption = enumOptions.find((item) => item.value === optionValues[key])}
                <Select.Root
                  type="single"
                  value={selectedOption?.selectValue ?? ""}
                  onValueChange={(selectValue) => {
                    const selected = enumOptions.find((item) => item.selectValue === selectValue)
                    if (selected) optionValues[key] = selected.value
                  }}
                >
                  <Select.Trigger id={`provider-option-${key}`} aria-required={opt.required || undefined} aria-describedby={opt.description ? `provider-option-help-${key}` : undefined} class="w-full h-9" disabled={saving}>
                    <span>{selectedOption ? selectedOption.label : optionValues[key] || "-- Select --"}</span>
                  </Select.Trigger>
                  <Select.Content>
                    {#each enumOptions as option, index (index)}
                      <Select.Item value={option.selectValue} label={option.label}>
                        {option.label}
                      </Select.Item>
                    {/each}
                  </Select.Content>
                </Select.Root>
              {:else}
                <Input
                  id={`provider-option-${key}`}
                  aria-required={opt.required || undefined} aria-describedby={opt.description ? `provider-option-help-${key}` : undefined}
                  type={opt.password ? "password" : "text"}
                  placeholder={opt.default != null ? String(opt.default) : ""}
                  value={optionValues[key] ?? ""}
                  oninput={(e) => (optionValues[key] = e.currentTarget.value)}
                  class="h-9"
                  disabled={saving}
                />
              {/if}
            </div>
          {/each}
        {/each}
      {/if}
    </div>

    </section>
    </div>

    {#if isDirty}
      <Sheet.Footer class="shrink-0 border-t p-6">
        <div class="flex flex-wrap items-center gap-2">
          <Button
            onclick={handleSaveOptions}
            disabled={saving || togglingSingleMachine}
            size="sm"
          >
            {saving ? "Saving..." : "Save"}
          </Button>
          <Button variant="outline" size="sm" onclick={() => { optionValues = { ...initialValues } }} disabled={saving}>
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

<UpdateConfirmDialog
  bind:open={confirmUpdateOpen}
  providerName={provider.name}
  currentVersion={provider.version}
  latestVersion={$providerVersions.updates[provider.name]?.latest}
  loading={updating}
  onconfirm={runUpdate}
/>

<ConfirmDialog
  bind:open={confirmSwitchOpen}
  title={`Switch '${provider.name}' from ${provider.version ?? ""} to ${targetTag}`}
  description={`Workspaces created with ${provider.version ?? ""} may behave differently after this change. Existing workspaces will run against ${targetTag} the next time DevSy uses them.`}
  confirmLabel="Switch"
  loading={switching}
  onconfirm={runSwitch}
/>
{/if}
