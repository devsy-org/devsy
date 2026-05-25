<script lang="ts">
import { onMount, onDestroy } from "svelte"
import { get } from "svelte/store"
import { Check, Circle, AlertCircle, Loader2 } from "@lucide/svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { Label } from "$lib/components/ui/label/index.js"
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
  providerInitStreaming,
  providerList,
  providerOptions,
  providerSetOptions,
  providerUse,
} from "$lib/ipc/commands.js"
import { onCommandProgress } from "$lib/ipc/events.js"
import { providers } from "$lib/stores/providers.js"
import { toasts } from "$lib/stores/toasts.js"
import { extractErrorMessage } from "$lib/utils/error.js"
import { isCommandSuccess } from "$lib/utils/log-parser.js"
import type { ProviderOption } from "$lib/types/index.js"
import type { UnlistenFn } from "$lib/ipc/types.js"

const PRESETS = [
  { name: "docker", description: "Local Docker containers" },
  { name: "podman", description: "Local Podman containers" },
  { name: "ssh", description: "Remote SSH machines" },
  { name: "kubernetes", description: "Kubernetes clusters" },
  { name: "aws", description: "Amazon Web Services" },
  { name: "gcloud", description: "Google Cloud Platform" },
  { name: "azure", description: "Microsoft Azure" },
  { name: "digitalocean", description: "DigitalOcean Droplets" },
]

type Step = "select" | "configure" | "initialize" | "complete"
type StepState = "pending" | "active" | "complete" | "error"

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
let adding = $state(false)

// Step 2 state
let providerName = $state("")
let options = $state<Record<string, ProviderOption>>({})
let optionValues = $state<Record<string, string>>({})
let loadingOptions = $state(false)
let saving = $state(false)

// Step 3 state
let initCommandId = $state<string | null>(null)
let initLines = $state<string[]>([])
let initRunning = $state(false)
let initError = $state<CLIError | null>(null)
let initStartError = $state("")

// Event listener cleanup
let unlisten: UnlistenFn | null = null

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
  if (initError) states.initialize = "error"
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
  currentStep = "select"
  error = ""
  source = ""
  customName = ""
  adding = false
  providerName = ""
  options = {}
  optionValues = {}
  loadingOptions = false
  saving = false
  initCommandId = null
  initLines = []
  initRunning = false
  initError = null
  initStartError = ""
}

$effect(() => {
  if (open && initialSource) {
    source = initialSource
  }
  if (!open) {
    reset()
  }
})

onMount(async () => {
  unlisten = await onCommandProgress((progress) => {
    if (initCommandId && progress.commandId === initCommandId) {
      if (progress.message) {
        initLines = [...initLines, progress.message]
      }
      if (progress.done) {
        initRunning = false
        if (isCommandSuccess(progress.message)) {
          refreshAndComplete()
        } else {
          initError =
            progress.cliError ?? {
              code: "UNKNOWN",
              message: "Provider initialization failed.",
            }
        }
      }
    }
  })
})

onDestroy(() => {
  unlisten?.()
})

function nameExists(name: string): boolean {
  return get(providers).some((p) => p.name === name)
}

async function handleSelect() {
  const src = source.trim()
  if (!src) {
    error = "Provider source is required"
    return
  }
  const name = customName.trim() || src
  if (nameExists(name)) {
    error = `Provider "${name}" already exists. Choose a different name.`
    return
  }

  error = ""
  adding = true
  try {
    await providerAdd(name, name !== src ? src : undefined)
    providerName = name
  } catch (err) {
    const msg = extractErrorMessage(err)
    if (msg.includes("already exists")) {
      error = `Provider "${name}" already exists. Choose a different name.`
    } else {
      error = `Failed to add provider: ${msg}`
    }
    adding = false
    return
  }

  const loaded = await loadProviderOptions()
  adding = false
  if (!loaded) {
    // Provider was created but we couldn't load its options. Stay on the
    // select step so the user sees the error and can retry/back out.
    return
  }

  if (requiredOptions.length === 0 || !hasUnfilledRequired) {
    currentStep = "initialize"
    startInit()
  } else {
    currentStep = "configure"
  }
}

async function loadProviderOptions(): Promise<boolean> {
  loadingOptions = true
  try {
    const raw = await providerOptions(providerName)
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
    return true
  } catch (err) {
    error = `Failed to load options: ${extractErrorMessage(err)}`
    return false
  } finally {
    loadingOptions = false
  }
}

async function handleConfigure() {
  if (hasUnfilledRequired) {
    error = "Please fill in all required fields."
    return
  }
  error = ""
  saving = true
  try {
    const values: Record<string, string> = {}
    for (const [key, val] of Object.entries(optionValues)) {
      if (val !== "") values[key] = val
    }
    await providerSetOptions(providerName, values)
  } catch (err) {
    error = `Failed to save options: ${extractErrorMessage(err)}`
    saving = false
    return
  }
  saving = false
  currentStep = "initialize"
  startInit()
}

async function startInit() {
  initRunning = true
  initError = null
  initStartError = ""
  initLines = []
  try {
    initCommandId = await providerInitStreaming(providerName)
  } catch (err) {
    initRunning = false
    initStartError = `Failed to start initialization: ${err instanceof Error ? err.message : String(err)}`
  }
}

async function refreshAndComplete() {
  try {
    const updated = await providerList()
    providers.set(updated)
  } catch (err) {
    toasts.error(`Failed to refresh providers: ${extractErrorMessage(err)}`)
  }
  currentStep = "complete"
}

function handleSkipInit() {
  refreshAndComplete()
}

async function handleSetDefault() {
  try {
    await providerUse(providerName)
    const updated = await providerList()
    providers.set(updated)
    toasts.success(`Set ${providerName} as default provider`)
  } catch (err) {
    toasts.error(`Failed to set default: ${extractErrorMessage(err)}`)
  }
}

function handleDone() {
  open = false
  oncomplete?.(providerName)
}
</script>

<Dialog.Root bind:open>
  <Dialog.Content class="sm:max-w-lg max-h-[85vh] flex flex-col gap-0 p-0 overflow-hidden">
    <Dialog.Header class="sr-only">
      <Dialog.Title>Add Provider</Dialog.Title>
      <Dialog.Description>
        Step-by-step wizard to add and initialize a new provider.
      </Dialog.Description>
    </Dialog.Header>

    <!-- Progress bar -->
    <div class="px-6 pt-5">
      <Progress value={progressValue} max={100} class="h-1" />
    </div>

    <!-- Step indicator -->
    <div class="flex items-center justify-between px-6 pt-4 pb-2">
      {#each STEPS as step, i (step.id)}
        <div class="flex items-center gap-1.5">
          <div
            class="flex h-6 w-6 items-center justify-center rounded-full text-xs font-medium
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
          </div>
          <span class="text-xs font-medium {stepStates[step.id] === 'active' ? 'text-foreground' : 'text-muted-foreground'}">
            {step.label}
          </span>
        </div>
        {#if i < STEPS.length - 1}
          <div class="mx-1 h-px flex-1 bg-border"></div>
        {/if}
      {/each}
    </div>

    <!-- Step content -->
    <div class="flex-1 overflow-y-auto px-6 pb-6 pt-2">
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

          <div class="grid grid-cols-2 gap-2">
            {#each PRESETS as p (p.name)}
              <button
                type="button"
                class="flex items-center gap-2.5 rounded-lg border p-3 text-left transition-colors hover:bg-accent/50
                  {source === p.name ? 'border-primary ring-1 ring-primary' : ''}"
                disabled={adding}
                onclick={() => { source = p.name; customName = "" }}
              >
                <ProviderIcon name={p.name} class="size-6 shrink-0" />
                <div>
                  <div class="text-sm font-medium">{p.name}</div>
                  <div class="text-xs text-muted-foreground">{p.description}</div>
                </div>
              </button>
            {/each}
          </div>

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
              <Label>Provider Source</Label>
              <Input
                placeholder="github.com/org/provider"
                value={source}
                oninput={(e) => { source = e.currentTarget.value; error = "" }}
                disabled={adding}
              />
            </div>
            <div class="space-y-1.5">
              <Label>Name <span class="text-muted-foreground font-normal">(optional)</span></Label>
              <Input
                placeholder="Defaults to source name"
                value={customName}
                oninput={(e) => { customName = e.currentTarget.value; error = "" }}
                disabled={adding}
              />
            </div>
          </div>

          <Button class="w-full" disabled={adding || !source.trim()} onclick={handleSelect}>
            {#if adding}
              <Loader2 class="mr-2 h-4 w-4 animate-spin" />
              Adding...
            {:else}
              Continue
            {/if}
          </Button>
        </div>

      {:else if currentStep === "configure"}
        <div class="space-y-4">
          <div>
            <h2 class="text-lg font-semibold">Configure {providerName}</h2>
            <p class="text-sm text-muted-foreground">Fill in the required options to set up your provider.</p>
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
            <div class="space-y-3">
              {#each visibleOptions as [key, opt] (key)}
                <div class="space-y-1.5">
                  <Label class="flex items-center gap-1">
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
                      onValueChange={(v) => { optionValues[key] = v ?? ""; error = "" }}
                    >
                      <Select.Trigger class="w-full">
                        {optionValues[key] || "Select..."}
                      </Select.Trigger>
                      <Select.Content>
                        {#each opt.enum as val (val)}
                          <Select.Item value={val}>{val}</Select.Item>
                        {/each}
                      </Select.Content>
                    </Select.Root>
                  {:else}
                    <Input
                      type={opt.password ? "password" : "text"}
                      placeholder={opt.default != null ? String(opt.default) : ""}
                      value={optionValues[key] ?? ""}
                      oninput={(e) => { optionValues[key] = e.currentTarget.value; error = "" }}
                      class={opt.required && !optionValues[key]?.trim() ? "border-amber-500" : ""}
                      disabled={saving}
                    />
                  {/if}
                </div>
              {/each}
            </div>

            <Button class="w-full" disabled={saving || hasUnfilledRequired} onclick={handleConfigure}>
              {#if saving}
                <Loader2 class="mr-2 h-4 w-4 animate-spin" />
                Saving...
              {:else}
                Continue
              {/if}
            </Button>
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
              {:else}
                Initialization complete.
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
                <div class="max-h-48 overflow-y-auto border-t">
                  <LogTable lines={initLines} />
                </div>
              </details>
            {:else}
              <div class="max-h-48 overflow-y-auto rounded-md border bg-muted/30">
                <LogTable lines={initLines} />
              </div>
            {/if}
          {:else if initRunning}
            <div class="flex items-center justify-center py-8">
              <Spinner class="size-6" />
            </div>
          {/if}

          <div class="flex gap-2">
            {#if initError}
              <Button variant="outline" class="flex-1" onclick={startInit}>
                Retry
              </Button>
              <Button variant="outline" class="flex-1" onclick={handleSkipInit}>
                Skip
              </Button>
            {:else if !initRunning}
              <Button class="w-full" onclick={refreshAndComplete}>
                Continue
              </Button>
            {/if}
          </div>
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
                {providerName} is ready to use.
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

          <div class="flex gap-2">
            <Button variant="outline" class="flex-1" onclick={handleSetDefault}>
              Set as Default
            </Button>
            <Button class="flex-1" onclick={handleDone}>
              Done
            </Button>
          </div>
        </div>
      {/if}
    </div>
  </Dialog.Content>
</Dialog.Root>
