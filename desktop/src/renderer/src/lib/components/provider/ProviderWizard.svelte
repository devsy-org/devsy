<script lang="ts">
import { onMount, onDestroy } from "svelte"
import { get } from "svelte/store"
import { Check, Circle, AlertCircle, Loader2 } from "@lucide/svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { Label } from "$lib/components/ui/label/index.js"
import { Switch } from "$lib/components/ui/switch/index.js"
import * as Select from "$lib/components/ui/select/index.js"
import * as Dialog from "$lib/components/ui/dialog/index.js"
import * as Alert from "$lib/components/ui/alert/index.js"
import { Progress } from "$lib/components/ui/progress/index.js"
import { Spinner } from "$lib/components/ui/spinner/index.js"
import ProviderIcon from "./ProviderIcon.svelte"
import LogTable from "$lib/components/log/LogTable.svelte"
import ErrorCard from "$lib/components/ErrorCard.svelte"
import type { CLIError } from "$shared/cli-error.js"
import {
  providerAdd,
  providerAvailable,
  providerInitStreaming,
  providerList,
  providerOptions,
  providerReleaseJob,
  providerSetOptions,
} from "$lib/ipc/commands.js"
import { onCommandProgress } from "$lib/ipc/events.js"
import { providers } from "$lib/stores/providers.js"
import { toasts } from "$lib/stores/toasts.js"
import { extractErrorMessage } from "$lib/utils/error.js"
import { isCommandSuccess } from "$lib/utils/log-parser.js"
import {
  initialProviderOptionValue,
  normalizeProviderOptionEnums,
  providerOptionEnumHasValue,
} from "$lib/utils/provider-option-enum.js"
import type { ProviderOption } from "$lib/types/index.js"
import type { UnlistenFn } from "$lib/ipc/types.js"

type ProviderPreset = {
  name: string
  description: string
  category: "Local" | "Remote" | "Cloud"
}

const PRESETS: ProviderPreset[] = [
  { name: "docker", description: "Local Docker containers", category: "Local" },
  { name: "podman", description: "Local Podman containers", category: "Local" },
  {
    name: "apple",
    description: "Apple containers (macOS 26+, Apple silicon)",
    category: "Local",
  },
  {
    name: "microsandbox",
    description: "Hardware-isolated microVMs",
    category: "Local",
  },
  {
    name: "lima",
    description: "Linux VMs via Lima (macOS)",
    category: "Local",
  },
  {
    name: "orbstack",
    description: "Fast containers and VMs via OrbStack (macOS)",
    category: "Local",
  },
  { name: "ssh", description: "Remote SSH machines", category: "Remote" },
  {
    name: "kubernetes",
    description: "Kubernetes clusters",
    category: "Remote",
  },
  { name: "aws", description: "Amazon Web Services", category: "Cloud" },
  { name: "gcloud", description: "Google Cloud Platform", category: "Cloud" },
  { name: "azure", description: "Microsoft Azure", category: "Cloud" },
  {
    name: "digitalocean",
    description: "DigitalOcean Droplets",
    category: "Cloud",
  },
]

type Step = "select" | "configure" | "initialize" | "complete"
type StepState = "pending" | "active" | "complete" | "error"
type SetupSession = {
  providerName: string
  installed: boolean
  initStarted: boolean
  releaseAttempted: boolean
  commandId: string | null
}

function newSetupSession(): SetupSession {
  return {
    providerName: "",
    installed: false,
    initStarted: false,
    releaseAttempted: false,
    commandId: null,
  }
}

const STEPS: { id: Step; label: string }[] = [
  { id: "select", label: "Select" },
  { id: "configure", label: "Configure" },
  { id: "initialize", label: "Initialize" },
  { id: "complete", label: "Done" },
]

let {
  open = $bindable(false),
  initialSource,
  oncomplete,
}: {
  open: boolean
  initialSource?: string
  oncomplete?: (name: string) => void
} = $props()

let currentStep = $state<Step>("select")
let error = $state("")

// Step 1 state
let source = $state("")
let customName = $state("")
let selectedProviderOption = $state(false)
let singleMachine = $state(false)
let adding = $state(false)
let needsCustomName = $derived(!!source.trim() && !selectedProviderOption)
let availableProviders = $state<string[]>([])
let loadingAvailableProviders = $state(false)
let availableProvidersError = $state("")

// Step 2 state
let providerName = $state("")
let options = $state<Record<string, ProviderOption>>({})
let optionValues = $state<Record<string, string>>({})
let editedOptionKeys = $state<Record<string, true>>({})
let loadingOptions = $state(false)
let optionsLoaded = $state(false)
let saving = $state(false)

// Step 3 state
let initLines = $state<string[]>([])
let initRunning = $state(false)
let initError = $state<CLIError | null>(null)
let initStartError = $state("")
let initSucceeded = $state(false)

// Event listener cleanup
let unlisten: UnlistenFn | null = null
let mounted = false
let activeSession = newSetupSession()

function isCurrentSession(session: SetupSession): boolean {
  return mounted && open && activeSession === session
}

function releaseProviderJob(
  session: SetupSession,
  allowAfterInit = false,
): Promise<void> {
  if (
    !session.providerName ||
    !session.installed ||
    session.releaseAttempted ||
    (session.initStarted && !allowAfterInit)
  ) {
    return Promise.resolve()
  }
  session.releaseAttempted = true
  return providerReleaseJob(session.providerName).catch(() => undefined)
}

let additionalProviders = $derived.by(() => {
  const presetNames = new Set(PRESETS.map((preset) => preset.name))
  const seen = new Set<string>()
  return availableProviders.flatMap((rawName) => {
    const name = typeof rawName === "string" ? rawName.trim() : ""
    const normalized = name.toLowerCase()
    if (!name || presetNames.has(normalized) || seen.has(normalized)) return []
    seen.add(normalized)
    return [name]
  })
})

let stepStates = $derived.by(() => {
  const states: Record<Step, StepState> = {
    select: "pending",
    configure: "pending",
    initialize: "pending",
    complete: "pending",
  }
  const stepOrder: Step[] = ["select", "configure", "initialize", "complete"]
  const currentIdx = stepOrder.indexOf(currentStep)
  for (let i = 0; i < stepOrder.length; i++) {
    if (i < currentIdx) states[stepOrder[i]] = "complete"
    else if (i === currentIdx) states[stepOrder[i]] = "active"
  }
  if (error && currentStep !== "initialize") states[currentStep] = "error"
  if (initError || initStartError) states.initialize = "error"
  return states
})

let progressValue = $derived(
  STEPS.findIndex((s) => s.id === currentStep) * 25 +
    (currentStep === "complete" ? 25 : 0),
)

let requiredOptions = $derived(
  Object.entries(options).filter(([, opt]) => opt.required && !opt.hidden),
)

let hasUnfilledRequired = $derived(
  requiredOptions.some(([key]) => !optionValues[key]?.trim()),
)

let visibleOptions = $derived(
  Object.entries(options).filter(([, opt]) => !opt.hidden),
)

function reset() {
  const previousSession = activeSession
  if (currentStep !== "complete") {
    releaseProviderJob(previousSession)
  }
  activeSession = newSetupSession()
  currentStep = "select"
  error = ""
  source = ""
  customName = ""
  selectedProviderOption = false
  singleMachine = false
  adding = false
  providerName = ""
  options = {}
  optionValues = {}
  editedOptionKeys = {}
  loadingOptions = false
  saving = false
  initLines = []
  initRunning = false
  initError = null
  initStartError = ""
  optionsLoaded = false
  initSucceeded = false
}

$effect(() => {
  if (open && initialSource) {
    source = initialSource
    selectedProviderOption = false
  }
  if (!open) {
    reset()
  }
})

onMount(async () => {
  mounted = true
  void loadAvailableProviders()
  const stopListening = await onCommandProgress((progress) => {
    if (
      mounted &&
      isCurrentSession(activeSession) &&
      activeSession.commandId &&
      progress.commandId === activeSession.commandId
    ) {
      if (progress.message) {
        initLines = [...initLines, progress.message]
      }
      if (progress.done) {
        initRunning = false
        if (isCommandSuccess(progress.success)) {
          initSucceeded = true
          void refreshAndComplete(activeSession)
        } else {
          initError = progress.cliError ?? {
            code: "UNKNOWN",
            message: "Provider initialization failed.",
          }
        }
      }
    }
  })
  if (mounted) unlisten = stopListening
  else stopListening()
})

onDestroy(() => {
  mounted = false
  releaseProviderJob(activeSession)
  unlisten?.()
})

async function loadAvailableProviders() {
  loadingAvailableProviders = true
  availableProvidersError = ""
  try {
    const names = await providerAvailable()
    if (mounted) availableProviders = Array.isArray(names) ? names : []
  } catch (err) {
    if (mounted) {
      availableProvidersError = `Could not load additional providers: ${extractErrorMessage(err)}`
    }
  } finally {
    if (mounted) loadingAvailableProviders = false
  }
}

function nameExists(name: string): boolean {
  return get(providers).some((p) => p.name === name)
}

async function handleSelect() {
  const operationSession = activeSession
  const src = source.trim()
  if (!src) {
    error = "Provider source is required"
    return
  }
  if (needsCustomName && !customName.trim()) {
    error = "Enter an installation name for this custom source."
    return
  }
  const name = customName.trim() || src
  const providerSource =
    selectedProviderOption && name === src ? undefined : src
  if (nameExists(name)) {
    error = `Provider "${name}" already exists. Choose a different name.`
    return
  }

  error = ""
  adding = true
  try {
    await providerAdd(name, providerSource, singleMachine)
    operationSession.providerName = name
    operationSession.installed = true
    if (!isCurrentSession(operationSession)) {
      releaseProviderJob(operationSession)
      return
    }
    providerName = name
  } catch (err) {
    if (!isCurrentSession(operationSession)) return
    const msg = extractErrorMessage(err)
    if (msg.includes("already exists")) {
      error = `Provider "${name}" already exists. Choose a different name.`
    } else {
      error = `Failed to add provider: ${msg}`
    }
    adding = false
    return
  }

  const loaded = await loadProviderOptions(operationSession)
  if (!isCurrentSession(operationSession)) return
  adding = false
  currentStep = "configure"
  if (!loaded) return
  if (visibleOptions.length === 0) {
    currentStep = "initialize"
    void startInit(operationSession)
  }
}

async function loadProviderOptions(
  operationSession = activeSession,
): Promise<boolean> {
  loadingOptions = true
  try {
    const raw = await providerOptions(providerName)
    if (!isCurrentSession(operationSession)) return false
    options = raw as unknown as Record<string, ProviderOption>
    optionsLoaded = true
    const values: Record<string, string> = {}
    for (const [key, opt] of Object.entries(options)) {
      const value = initialProviderOptionValue(opt)
      if (value !== undefined) values[key] = value
    }
    optionValues = values
    editedOptionKeys = {}
    return true
  } catch (err) {
    if (!isCurrentSession(operationSession)) return false
    optionsLoaded = false
    error = `Failed to load options: ${extractErrorMessage(err)}`
    return false
  } finally {
    if (isCurrentSession(operationSession)) loadingOptions = false
  }
}

async function retryLoadProviderOptions() {
  const operationSession = activeSession
  error = ""
  await loadProviderOptions(operationSession)
}

async function handleConfigure() {
  const operationSession = activeSession
  if (!optionsLoaded) return
  if (visibleOptions.length === 0) {
    error = ""
    currentStep = "initialize"
    void startInit(operationSession)
    return
  }
  if (hasUnfilledRequired) {
    error = "Please fill in all required fields."
    return
  }
  error = ""
  saving = true
  try {
    const values: Record<string, string> = {}
    for (const [key, val] of Object.entries(optionValues)) {
      if (
        editedOptionKeys[key] &&
        !options[key]?.hidden &&
        (val !== "" || providerOptionEnumHasValue(options[key]?.enum, ""))
      ) {
        values[key] = val
      }
    }
    if (Object.keys(values).length > 0) {
      await providerSetOptions(providerName, values, singleMachine)
    }
  } catch (err) {
    if (!isCurrentSession(operationSession)) return
    error = `Failed to save options: ${extractErrorMessage(err)}`
    saving = false
    return
  }
  if (!isCurrentSession(operationSession)) return
  saving = false
  currentStep = "initialize"
  void startInit(operationSession)
}

async function startInit(operationSession = activeSession) {
  if (!isCurrentSession(operationSession)) return
  initRunning = true
  operationSession.initStarted = true
  initError = null
  initStartError = ""
  initSucceeded = false
  initLines = []
  try {
    const commandId = await providerInitStreaming(providerName)
    operationSession.commandId = commandId
    if (!isCurrentSession(operationSession)) return
  } catch (err) {
    operationSession.initStarted = false
    operationSession.commandId = null
    if (!isCurrentSession(operationSession)) {
      releaseProviderJob(operationSession)
      return
    }
    initRunning = false
    initStartError = `Failed to start initialization: ${err instanceof Error ? err.message : String(err)}`
  }
}

async function refreshAndComplete(operationSession: SetupSession) {
  try {
    const updated = await providerList()
    if (!isCurrentSession(operationSession)) return
    providers.set(updated)
  } catch (err) {
    if (!isCurrentSession(operationSession)) return
    toasts.error(`Failed to refresh providers: ${extractErrorMessage(err)}`)
  }
  if (!isCurrentSession(operationSession)) return
  currentStep = "complete"
}

// Skipping init leaves the provider installed but uninitialized, which is a
// settled state: release the job so the card shows that rather than staying
// busy on the install that already finished.
async function handleSkipInit() {
  const operationSession = activeSession
  initSucceeded = false
  await releaseProviderJob(operationSession, true)
  if (!isCurrentSession(operationSession)) return
  await refreshAndComplete(operationSession)
}

function handleDone() {
  open = false
  oncomplete?.(providerName)
}
</script>

<Dialog.Root bind:open>
  <Dialog.Content class="sm:max-w-2xl max-h-[90vh] flex flex-col gap-0 p-0 overflow-hidden">
    <Dialog.Header class="shrink-0 border-b px-6 py-4 text-left">
      <Dialog.Title class="text-lg font-semibold">Add Provider</Dialog.Title>
      <Dialog.Description>
        Choose a provider, review its settings, then initialize it when you are ready.
      </Dialog.Description>
    </Dialog.Header>

    <!-- Progress bar -->
    <div class="shrink-0 px-6 pt-4">
      <Progress
        value={progressValue}
        max={100}
        class="h-1"
        aria-label="Provider setup progress"
      />
    </div>

    <!-- Step indicator -->
    <ol aria-label="Provider setup steps" class="shrink-0 flex items-center justify-between gap-2 px-6 py-3">
      {#each STEPS as step, i (step.id)}
        <li class="flex min-w-0 items-center gap-1.5" aria-current={currentStep === step.id ? "step" : undefined}>
          <span
            aria-hidden="true"
            class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-xs font-medium
              {stepStates[step.id] === 'complete' ? 'bg-primary text-primary-foreground' : ''}
              {stepStates[step.id] === 'active' ? 'bg-primary text-primary-foreground' : ''}
              {stepStates[step.id] === 'error' ? 'bg-destructive text-destructive-foreground' : ''}
              {stepStates[step.id] === 'pending' ? 'bg-muted text-muted-foreground' : ''}"
          >
            {#if stepStates[step.id] === "complete"}
              <Check class="h-3.5 w-3.5" />
            {:else if stepStates[step.id] === "error"}
              <AlertCircle class="h-3.5 w-3.5" />
            {:else}
              {i + 1}
            {/if}
          </span>
          <span class="truncate text-xs font-medium {stepStates[step.id] === 'active' ? 'text-foreground' : 'text-muted-foreground'}">{step.label}</span>
        </li>
      {/each}
    </ol>

    <!-- Step content -->
    <div class="min-h-0 flex-1 overflow-y-auto px-6 py-3">
      {#if currentStep === "select"}
        <div class="space-y-4">
          <div>
            <h2 class="text-lg font-semibold">Select a Provider</h2>
            <p class="text-sm text-muted-foreground">Choose a preset or enter a custom provider source.</p>
          </div>

          {#if error}
            <Alert.Root variant="destructive">
              <AlertCircle class="h-4 w-4" />
              <Alert.Description>{error}</Alert.Description>
            </Alert.Root>
          {/if}

          {#each ["Local", "Remote", "Cloud"] as category}
            <section aria-labelledby="preset-{category.toLowerCase()}" class="space-y-2">
              <h3 id="preset-{category.toLowerCase()}" class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{category}</h3>
              <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
                {#each PRESETS.filter((preset) => preset.category === category) as p (p.name)}
                  <button
                    type="button"
                    aria-pressed={selectedProviderOption && source === p.name}
                    class="flex min-h-14 items-center gap-2.5 rounded-lg border p-2.5 text-left transition-colors hover:bg-accent/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring
                      {selectedProviderOption && source === p.name ? 'border-primary bg-accent/40 ring-1 ring-primary' : ''}"
                    disabled={adding}
                    onclick={() => { source = p.name; selectedProviderOption = true; customName = ""; error = "" }}
                  >
                    <ProviderIcon name={p.name} class="size-6 shrink-0" />
                    <span class="min-w-0">
                      <span class="block text-sm font-medium">{p.name}</span>
                      <span class="block text-xs text-muted-foreground">{p.description}</span>
                    </span>
                  </button>
                {/each}
              </div>
            </section>
          {/each}

          {#if additionalProviders.length > 0 || loadingAvailableProviders || availableProvidersError}
            <section aria-labelledby="preset-more" class="space-y-2">
              <div>
                <h3 id="preset-more" class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">More providers</h3>
                <p class="text-xs text-muted-foreground">Discovered from Devsy’s provider list. Availability and setup requirements vary by provider.</p>
              </div>
              {#if loadingAvailableProviders}
                <p role="status" class="text-xs text-muted-foreground">Loading additional providers…</p>
              {:else if availableProvidersError}
                <div class="flex flex-wrap items-center gap-2">
                  <p role="alert" class="text-xs text-destructive">{availableProvidersError}</p>
                  <Button variant="outline" size="sm" onclick={loadAvailableProviders}>Retry provider list</Button>
                </div>
              {:else}
                <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
                  {#each additionalProviders as name (name.toLowerCase())}
                    <button
                      type="button"
                      aria-pressed={selectedProviderOption && source === name}
                      class="flex min-h-14 items-center gap-2.5 rounded-lg border p-2.5 text-left transition-colors hover:bg-accent/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring
                        {selectedProviderOption && source === name ? 'border-primary bg-accent/40 ring-1 ring-primary' : ''}"
                      disabled={adding}
                      onclick={() => { source = name; selectedProviderOption = true; customName = ""; error = "" }}
                    >
                      <ProviderIcon name={name} class="size-6 shrink-0" />
                      <span class="min-w-0">
                        <span class="block text-sm font-medium">{name}</span>
                        <span class="block text-xs text-muted-foreground">Listed by Devsy</span>
                      </span>
                    </button>
                  {/each}
                </div>
              {/if}
            </section>
          {/if}

          <div class="relative">
            <div class="absolute inset-0 flex items-center">
              <span class="w-full border-t"></span>
            </div>
            <div class="relative flex justify-center text-xs uppercase">
              <span class="bg-popover px-2 text-muted-foreground">or custom source</span>
            </div>
          </div>

          <div class="space-y-3">
            <div class="space-y-1.5">
              <Label for="provider-source">Provider source</Label>
              <Input
                id="provider-source"
                placeholder="github.com/org/provider"
                value={source}
                oninput={(e) => { source = e.currentTarget.value; selectedProviderOption = false; error = "" }}
                disabled={adding}
              />
            </div>
            <div class="space-y-1.5">
              <Label for="provider-name">Name <span class="text-muted-foreground font-normal">({needsCustomName ? "required for custom source" : "optional"})</span></Label>
              <Input
                id="provider-name"
                aria-required={needsCustomName || undefined}
                aria-describedby="provider-name-help"
                placeholder="Defaults to source name"
                value={customName}
                oninput={(e) => { customName = e.currentTarget.value; error = "" }}
                disabled={adding}
              />
              <p id="provider-name-help" class="text-xs text-muted-foreground">{needsCustomName ? "Choose a name for this installation so its settings and initialization use the same provider." : "Leave blank to use the provider name above."}</p>
            </div>
            <div class="flex items-center justify-between gap-4">
              <div class="space-y-0.5">
                <Label for="reuse-machine">Reuse machine</Label>
                <p class="text-sm text-muted-foreground">
                  Run all workspaces on a single shared machine instead of one per workspace.
                </p>
              </div>
              <Switch
                id="reuse-machine"
                checked={singleMachine}
                onCheckedChange={(v) => { singleMachine = v }}
                disabled={adding}
              />
            </div>
          </div>

        </div>

      {:else if currentStep === "configure"}
        <div class="space-y-4">
          <div>
            <h2 class="text-lg font-semibold">Configure {providerName}</h2>
            <p class="text-sm text-muted-foreground">Review provider settings before initialization. Required fields are marked with an asterisk.</p>
          </div>

          {#if error}
            <Alert.Root variant="destructive">
              <AlertCircle class="h-4 w-4" />
              <Alert.Description>{error}</Alert.Description>
            </Alert.Root>
          {/if}

          {#if loadingOptions}
            <div class="flex items-center justify-center py-8">
              <Spinner class="size-6" />
            </div>
          {:else}
            {#if !optionsLoaded}
              <Button variant="outline" class="w-full" onclick={retryLoadProviderOptions}>
                Retry loading options
              </Button>
            {/if}
            <div class="space-y-3">
              {#each visibleOptions as [key, opt] (key)}
                <div class="space-y-1.5">
                  <Label for="provider-option-{key}" class="flex items-center gap-1">
                    {opt.displayName ?? opt.name ?? key}
                    {#if opt.required}
                      <span class="text-destructive">*</span>
                    {/if}
                  </Label>
                  {#if opt.description}
                    <p class="text-xs text-muted-foreground">{opt.description}</p>
                  {/if}
                  {#if opt.enum && opt.enum.length > 0}
                    {@const enumOptions = normalizeProviderOptionEnums(opt.enum)}
                    {@const selectedOption = enumOptions.find((item) => item.value === optionValues[key])}
                    <Select.Root
                      type="single"
                      value={selectedOption?.selectValue ?? ""}
                      onValueChange={(selectValue) => {
                        const selected = enumOptions.find((item) => item.selectValue === selectValue)
                        if (selected) {
                          optionValues[key] = selected.value
                          editedOptionKeys[key] = true
                        }
                        error = ""
                      }}
                    >
                      <Select.Trigger
                        id="provider-option-{key}"
                        class="w-full"
                        aria-label={opt.displayName ?? opt.name ?? key}
                      >
                        {selectedOption ? selectedOption.label : optionValues[key] || "Select..."}
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
                      id="provider-option-{key}"
                      type={opt.password ? "password" : "text"}
                      placeholder={opt.default != null ? String(opt.default) : ""}
                      value={optionValues[key] ?? ""}
                      oninput={(e) => {
                        optionValues[key] = e.currentTarget.value
                        editedOptionKeys[key] = true
                        error = ""
                      }}
                      class={opt.required && !optionValues[key]?.trim() ? "border-amber-500" : ""}
                      disabled={saving}
                    />
                  {/if}
                </div>
              {/each}
            </div>

          {/if}
        </div>

      {:else if currentStep === "initialize"}
        <div class="space-y-4">
          <div>
            <h2 class="text-lg font-semibold">Initializing {providerName}</h2>
            <p class="text-sm text-muted-foreground">
              {#if initRunning}
                Running provider initialization...
              {:else if initError}
                Initialization encountered an issue.
              {:else if initSucceeded}
                Initialization complete.
              {:else}
                Initialization has not completed.
              {/if}
            </p>
          </div>

          {#if initStartError}
            <Alert.Root variant="destructive">
              <AlertCircle class="h-4 w-4" />
              <Alert.Description>{initStartError}</Alert.Description>
            </Alert.Root>
          {/if}

          {#if initError}
            <ErrorCard cliError={initError} />
          {/if}

          {#if initLines.length > 0}
            {#if initError}
              <details class="rounded-md border bg-muted/30">
                <summary
                  class="cursor-pointer select-none px-3 py-2 text-sm font-medium text-muted-foreground hover:text-foreground"
                >
                  Show logs
                </summary>
                <LogTable lines={initLines} maxHeightClass="max-h-48" class="border-x-0 border-b-0 rounded-none" />
              </details>
            {:else}
              <LogTable lines={initLines} maxHeightClass="max-h-48" follow />
            {/if}
          {:else if initRunning}
            <div class="flex items-center justify-center py-8">
              <Spinner class="size-6" />
            </div>
          {/if}

        </div>

      {:else if currentStep === "complete"}
        <div class="space-y-6 py-4">
          <div class="flex flex-col items-center gap-3 text-center">
            <div class="flex h-12 w-12 items-center justify-center rounded-full bg-primary/10">
              <Check class="h-6 w-6 text-primary" />
            </div>
            <div>
              <h2 class="text-lg font-semibold">Provider Added</h2>
              <p class="text-sm text-muted-foreground">
                {#if initSucceeded}
                  {providerName} is ready to use.
                {:else}
                  {providerName} was added but is not initialized yet. You can initialize it from Providers.
                {/if}
              </p>
            </div>
          </div>

          <div class="flex items-center gap-3 rounded-lg border p-4">
            <ProviderIcon name={providerName} class="size-10" />
            <div>
              <div class="font-medium">{providerName}</div>
              <div class="text-sm text-muted-foreground">
                {get(providers).find((p) => p.name === providerName)?.state?.initialized
                  ? "Initialized"
                  : "Not initialized"}
              </div>
            </div>
          </div>

        </div>
      {/if}
    </div>

    <footer class="shrink-0 border-t bg-background px-6 py-4">
      {#if currentStep === "select"}
        <Button class="w-full" disabled={adding || !source.trim()} onclick={handleSelect}>
          {#if adding}<Loader2 class="mr-2 h-4 w-4 animate-spin" />Adding...{:else}Continue{/if}
        </Button>
      {:else if currentStep === "configure"}
        <Button class="w-full" disabled={saving || loadingOptions || !optionsLoaded || hasUnfilledRequired} onclick={handleConfigure}>
          {#if saving}<Loader2 class="mr-2 h-4 w-4 animate-spin" />Saving...{:else}Continue{/if}
        </Button>
      {:else if currentStep === "initialize"}
        {#if initError}
          <div class="flex gap-2">
            <Button variant="outline" class="flex-1" onclick={() => startInit()}>Retry</Button>
            <Button variant="outline" class="flex-1" onclick={handleSkipInit}>Skip initialization</Button>
          </div>
        {:else if !initRunning && initStartError}
          <div class="flex gap-2">
            <Button variant="outline" class="flex-1" onclick={() => startInit()}>Retry</Button>
            <Button variant="outline" class="flex-1" onclick={handleSkipInit}>Skip initialization</Button>
          </div>
        {/if}
      {:else if currentStep === "complete"}
        <Button class="w-full" onclick={handleDone}>Done</Button>
      {/if}
    </footer>
  </Dialog.Content>
</Dialog.Root>
