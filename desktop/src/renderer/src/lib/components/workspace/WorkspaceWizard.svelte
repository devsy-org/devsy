<script lang="ts">
import { onMount, onDestroy } from "svelte"
import { goto } from "$lib/router.js"
import {
  Check,
  ChevronsUpDown,
  AlertCircle,
  TriangleAlert,
  Loader2,
} from "@lucide/svelte"
import { Button } from "$lib/components/ui/button/index.js"
import * as Command from "$lib/components/ui/command/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { Label } from "$lib/components/ui/label/index.js"
import * as Popover from "$lib/components/ui/popover/index.js"
import * as Dialog from "$lib/components/ui/dialog/index.js"
import * as Alert from "$lib/components/ui/alert/index.js"
import { Progress } from "$lib/components/ui/progress/index.js"
import LanguageIcon from "$lib/components/workspace/LanguageIcon.svelte"
import ConfirmDialog from "$lib/components/layout/ConfirmDialog.svelte"
import LogTable from "$lib/components/log/LogTable.svelte"
import { workspaceUp } from "$lib/ipc/commands.js"
import { onCommandProgress } from "$lib/ipc/events.js"
import type { CommandProgress } from "$lib/types/index.js"
import { providers } from "$lib/stores/providers.js"
import { workspaces } from "$lib/stores/workspaces.js"
import { toasts } from "$lib/stores/toasts.js"
import { extractErrorMessage } from "$lib/utils/error.js"
import { isCommandSuccess, stripAnsi } from "$lib/utils/log-parser.js"
import type { UnlistenFn } from "$lib/ipc/types.js"

type Step = "provider" | "source" | "ide" | "review" | "launch"
type StepState = "pending" | "active" | "complete" | "error"

const STEPS: { id: Step; label: string }[] = [
  { id: "provider", label: "Provider" },
  { id: "source", label: "Source" },
  { id: "ide", label: "IDE" },
  { id: "review", label: "Review" },
  { id: "launch", label: "Launch" },
]

const ideIcon = (name: string) => `./icons/ides/${name}.svg`

const IDE_GROUPS = [
  {
    label: "Primary",
    options: [
      { value: "none", label: "None", icon: ideIcon("none") },
      { value: "vscode", label: "VS Code", icon: ideIcon("vscode") },
      { value: "openvscode", label: "VS Code Browser", icon: ideIcon("vscodebrowser") },
      { value: "code-server", label: "code-server", icon: ideIcon("code-server") },
      { value: "cursor", label: "Cursor", icon: ideIcon("cursor") },
      { value: "zed", label: "Zed", icon: ideIcon("zed") },
      { value: "codium", label: "VSCodium", icon: ideIcon("codium") },
      { value: "windsurf", label: "Windsurf Editor", icon: ideIcon("windsurf") },
      { value: "antigravity", label: "Google Antigravity", icon: ideIcon("antigravity") },
      { value: "bob", label: "IBM Bob", icon: ideIcon("bob") },
    ],
  },
  {
    label: "JetBrains",
    options: [
      { value: "intellij", label: "IntelliJ IDEA", icon: ideIcon("intellij") },
      { value: "pycharm", label: "PyCharm", icon: ideIcon("pycharm") },
      { value: "phpstorm", label: "PhpStorm", icon: ideIcon("phpstorm") },
      { value: "rider", label: "Rider", icon: ideIcon("rider") },
      { value: "fleet", label: "Fleet", icon: ideIcon("fleet") },
      { value: "goland", label: "GoLand", icon: ideIcon("goland") },
      { value: "webstorm", label: "WebStorm", icon: ideIcon("webstorm") },
      { value: "rustrover", label: "RustRover", icon: ideIcon("rustrover") },
      { value: "rubymine", label: "RubyMine", icon: ideIcon("rubymine") },
      { value: "clion", label: "CLion", icon: ideIcon("clion") },
      { value: "dataspell", label: "DataSpell", icon: ideIcon("dataspell") },
    ],
  },
  {
    label: "Other",
    options: [
      { value: "jupyternotebook", label: "Jupyter Notebook", icon: ideIcon("jupyter") },
      { value: "vscode-insiders", label: "VS Code Insiders", icon: ideIcon("vscode_insiders") },
      { value: "positron", label: "Positron", icon: ideIcon("positron") },
      { value: "rstudio", label: "RStudio Server", icon: ideIcon("rstudio") },
    ],
  },
]

const ALL_IDES = IDE_GROUPS.flatMap((g) => g.options)

const TEMPLATES = [
  { name: "Python", source: "https://github.com/microsoft/vscode-remote-try-python" },
  { name: "Node.js", source: "https://github.com/microsoft/vscode-remote-try-node" },
  { name: "Go", source: "https://github.com/microsoft/vscode-remote-try-go" },
  { name: "Rust", source: "https://github.com/microsoft/vscode-remote-try-rust" },
  { name: "Java", source: "https://github.com/microsoft/vscode-remote-try-java" },
  { name: "PHP", source: "https://github.com/microsoft/vscode-remote-try-php" },
  { name: "C++", source: "https://github.com/microsoft/vscode-remote-try-cpp" },
  { name: ".NET", source: "https://github.com/microsoft/vscode-remote-try-dotnet" },
  { name: "Ruby", source: "https://github.com/skevetter/devsy-quickstart-ruby" },
]

const LAUNCH_TIMEOUT_MS = 10 * 60 * 1000

let {
  open = $bindable(false),
  oncomplete,
}: {
  open: boolean
  oncomplete?: (workspaceId: string) => void
} = $props()

let currentStep = $state<Step>("provider")

// Form state
let selectedProvider = $state("")
let source = $state("")
let workspaceFolder = $state("")
let advancedOpen = $state(false)
let selectedIde = $state("none")
let workspaceName = $state("")

// IDE combobox state
let ideComboOpen = $state(false)
let ideSearch = $state("")

// Launch state
let commandId = $state<string | null>(null)
let outputLines = $state<string[]>([])
let outputEl = $state<HTMLDivElement | null>(null)
let launchRunning = $state(false)
let launchError = $state("")
let launchSuccess = $state(false)
let launchedWorkspaceId = $state<string | null>(null)
let confirmCancelOpen = $state(false)
let unlisten: UnlistenFn | null = null
let watchdog: ReturnType<typeof setTimeout> | null = null

let initializedProviders = $derived(
  $providers.filter((p) => p.state?.initialized === true),
)

const selectedIdeEntry = $derived(ALL_IDES.find((i) => i.value === selectedIde))
const ideLabel = $derived(selectedIdeEntry?.label ?? "Select an IDE...")
const ideIconSrc = $derived(selectedIdeEntry?.icon)

let filteredIdes = $derived(
  ideSearch
    ? ALL_IDES.filter((i) =>
        i.label.toLowerCase().includes(ideSearch.toLowerCase()),
      )
    : ALL_IDES,
)

let resolvedId = $derived(
  workspaceName.trim() ||
    source
      .trim()
      .split("/")
      .pop()
      ?.replace(/\.git$/, "") ||
    "",
)

let resolvedIdInvalid = $derived(
  resolvedId === "" ||
    resolvedId === "." ||
    resolvedId.includes(":") ||
    !/^[A-Za-z0-9._-]+$/.test(resolvedId),
)

let nameConflict = $derived(
  resolvedId !== "" &&
    $workspaces.some(
      (ws) => ws.id.toLowerCase() === resolvedId.toLowerCase(),
    ),
)

let stepStates = $derived.by(() => {
  const states: Record<Step, StepState> = {
    provider: "pending",
    source: "pending",
    ide: "pending",
    review: "pending",
    launch: "pending",
  }
  const order: Step[] = ["provider", "source", "ide", "review", "launch"]
  const idx = order.indexOf(currentStep)
  for (let i = 0; i < order.length; i++) {
    if (i < idx) states[order[i]] = "complete"
    else if (i === idx) states[order[i]] = "active"
  }
  if (launchError) states.launch = "error"
  else if (launchSuccess) states.launch = "complete"
  return states
})

let progressValue = $derived(
  (STEPS.findIndex((s) => s.id === currentStep) / (STEPS.length - 1)) * 100,
)

function clearWatchdog() {
  if (watchdog) {
    clearTimeout(watchdog)
    watchdog = null
  }
}

function reset() {
  currentStep = "provider"
  selectedProvider = ""
  source = ""
  workspaceFolder = ""
  advancedOpen = false
  selectedIde = "none"
  workspaceName = ""
  ideComboOpen = false
  ideSearch = ""
  commandId = null
  outputLines = []
  launchRunning = false
  launchError = ""
  launchSuccess = false
  launchedWorkspaceId = null
  confirmCancelOpen = false
  clearWatchdog()
  unlisten?.()
  unlisten = null
}

$effect(() => {
  if (!open) {
    reset()
  }
})

onMount(() => {
  // Listener is registered at handleLaunch time (race-safe pattern).
})

onDestroy(() => {
  unlisten?.()
  clearWatchdog()
})

function handleProgress(progress: CommandProgress, wsId: string | undefined) {
  if (progress.message) {
    outputLines = [...outputLines, progress.message]
    requestAnimationFrame(() => {
      outputEl?.scrollIntoView?.({ block: "end", behavior: "smooth" })
    })
  }
  if (progress.done) {
    launchRunning = false
    clearWatchdog()
    if (isCommandSuccess(progress.message)) {
      launchSuccess = true
      launchedWorkspaceId = wsId ?? null
      toasts.success(`Workspace ${wsId ?? "created"} is ready`)
      if (wsId) oncomplete?.(wsId)
    } else {
      launchError = "Workspace creation failed. Check output for details."
      toasts.error(launchError)
    }
  }
}

async function handleLaunch() {
  if (resolvedIdInvalid || nameConflict) return
  launchRunning = true
  launchError = ""
  launchSuccess = false
  outputLines = []
  commandId = null
  launchedWorkspaceId = null
  clearWatchdog()

  const workspaceId = resolvedId

  try {
    const pendingEvents: CommandProgress[] = []
    let resolvedCommandId: string | null = null

    unlisten?.()
    unlisten = await onCommandProgress((progress) => {
      if (resolvedCommandId) {
        if (progress.commandId === resolvedCommandId) {
          handleProgress(progress, workspaceId)
        }
      } else {
        pendingEvents.push(progress)
      }
    })

    const cmdId = await workspaceUp({
      source: source.trim(),
      workspaceId,
      provider: selectedProvider || undefined,
      ide: selectedIde,
      ideLaunch: "auto",
      workspaceFolder: workspaceFolder.trim() || undefined,
      debug: true,
    })

    commandId = cmdId
    resolvedCommandId = cmdId

    for (const event of pendingEvents) {
      if (event.commandId === cmdId) {
        handleProgress(event, workspaceId)
      }
    }

    watchdog = setTimeout(() => {
      if (launchRunning) {
        launchRunning = false
        launchError =
          "Workspace creation timed out after 10 minutes. The process may still be running in the background."
        toasts.error(launchError)
        unlisten?.()
        unlisten = null
        commandId = null
        launchedWorkspaceId = null
      }
    }, LAUNCH_TIMEOUT_MS)
  } catch (err) {
    launchRunning = false
    launchError = `Failed to create workspace: ${extractErrorMessage(err)}`
    toasts.error(launchError)
    unlisten?.()
    unlisten = null
    commandId = null
  }
}

function handleOpenChange(newOpen: boolean) {
  if (!newOpen && launchRunning) {
    confirmCancelOpen = true
    return
  }
  open = newOpen
}

function goToStep(step: Step) {
  currentStep = step
}

function continueFromProvider() {
  if (!selectedProvider) return
  currentStep = "source"
}

function continueFromSource() {
  if (!source.trim()) return
  currentStep = "ide"
}

function continueFromIde() {
  currentStep = "review"
}

function continueFromReview() {
  if (resolvedIdInvalid || nameConflict) return
  currentStep = "launch"
  handleLaunch()
}

function selectTemplate(t: { name: string; source: string }) {
  source = t.source
  if (!workspaceName) {
    workspaceName = t.name.toLowerCase().replace(/[^a-z0-9]/g, "-")
  }
}
</script>

<Dialog.Root {open} onOpenChange={handleOpenChange}>
  <Dialog.Content class="sm:max-w-2xl max-h-[90vh] flex flex-col gap-0 p-0 overflow-hidden">
    <Dialog.Header class="sr-only">
      <Dialog.Title>Create Workspace</Dialog.Title>
      <Dialog.Description>
        Step-by-step wizard to create and launch a new workspace.
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
      {#if currentStep === "provider"}
        <div class="space-y-4">
          <div>
            <h2 class="text-lg font-semibold">Choose a Provider</h2>
            <p class="text-sm text-muted-foreground">Select an initialized provider to host your workspace.</p>
          </div>

          {#if initializedProviders.length === 0}
            <Alert.Root variant="destructive">
              <AlertCircle class="h-4 w-4" />
              <Alert.Description>
                At least one initialized provider is required to create a workspace.
              </Alert.Description>
            </Alert.Root>
            <Button
              variant="outline"
              onclick={() => { goto('/providers/add'); open = false }}
            >
              Add Provider
            </Button>
          {:else}
            <div class="space-y-2">
              {#each initializedProviders as p (p.name)}
                <button
                  type="button"
                  class="flex w-full items-center gap-3 rounded-lg border p-3 text-left transition-colors hover:bg-accent/50
                    {selectedProvider === p.name ? 'border-primary ring-1 ring-primary' : ''}"
                  onclick={() => { selectedProvider = p.name }}
                >
                  <Check class="h-4 w-4 {selectedProvider === p.name ? 'opacity-100 text-primary' : 'opacity-0'}" />
                  <div class="flex-1">
                    <div class="text-sm font-medium">{p.name}</div>
                    {#if p.description}
                      <div class="text-xs text-muted-foreground">{p.description}</div>
                    {/if}
                  </div>
                </button>
              {/each}
            </div>
          {/if}

          <div class="flex justify-end gap-2 pt-2">
            <Button
              disabled={!selectedProvider || initializedProviders.length === 0}
              onclick={continueFromProvider}
            >
              Continue
            </Button>
          </div>
        </div>

      {:else if currentStep === "source"}
        <div class="space-y-4">
          <div>
            <h2 class="text-lg font-semibold">Choose a Source</h2>
            <p class="text-sm text-muted-foreground">Pick a template or enter a custom source (git URL, image, or local path).</p>
          </div>

          <div class="space-y-2">
            <h3 class="text-xs font-medium uppercase tracking-wider text-muted-foreground">Quick Start Templates</h3>
            <div class="grid grid-cols-3 gap-2">
              {#each TEMPLATES as template (template.name)}
                <button
                  type="button"
                  class="flex flex-col items-center gap-1.5 rounded-lg border bg-card p-3 text-center text-sm transition-colors hover:bg-accent/50 active:scale-[0.98] {source === template.source ? 'border-primary ring-1 ring-primary' : ''}"
                  onclick={() => selectTemplate(template)}
                >
                  <LanguageIcon name={template.name} class="h-10 w-10" />
                  <span class="truncate text-xs">{template.name}</span>
                </button>
              {/each}
            </div>
          </div>

          <div class="space-y-1.5">
            <Label class="text-sm">Source *</Label>
            <Input
              placeholder="github.com/org/repo, local path, or image"
              value={source}
              oninput={(e) => (source = e.currentTarget.value)}
            />
          </div>

          <div>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onclick={() => (advancedOpen = !advancedOpen)}
            >
              {advancedOpen ? "Hide" : "Show"} advanced options
            </Button>
            {#if advancedOpen}
              <div class="mt-2 space-y-1.5">
                <Label class="text-sm">Workspace Folder</Label>
                <Input
                  placeholder="Subfolder within the source to use as workspace root"
                  value={workspaceFolder}
                  oninput={(e) => (workspaceFolder = e.currentTarget.value)}
                />
                <p class="text-xs text-muted-foreground">
                  Optional path inside the repository to use as the workspace root.
                </p>
              </div>
            {/if}
          </div>

          <div class="flex justify-between gap-2 pt-2">
            <Button variant="outline" onclick={() => goToStep("provider")}>Back</Button>
            <Button disabled={!source.trim()} onclick={continueFromSource}>Continue</Button>
          </div>
        </div>

      {:else if currentStep === "ide"}
        <div class="space-y-4">
          <div>
            <h2 class="text-lg font-semibold">Choose an IDE</h2>
            <p class="text-sm text-muted-foreground">Pick an IDE to open the workspace with. (Optional)</p>
          </div>

          <div class="space-y-1.5">
            <Label class="text-sm">IDE</Label>
            <Popover.Root bind:open={ideComboOpen}>
              <Popover.Trigger class="w-full">
                {#snippet child({ props })}
                  <Button variant="outline" class="h-9 w-full justify-between" {...props}>
                    <span class="flex items-center gap-2 flex-1 truncate text-left">
                      {#if ideIconSrc}
                        <img src={ideIconSrc} alt="" class="h-4 w-4 shrink-0" />
                      {/if}
                      <span class="truncate">{ideLabel}</span>
                    </span>
                    <ChevronsUpDown class="ml-2 h-4 w-4 shrink-0 opacity-50" />
                  </Button>
                {/snippet}
              </Popover.Trigger>
              <Popover.Content class="w-[var(--bits-popover-anchor-width)] p-0" align="start">
                <Command.Root shouldFilter={false}>
                  <Command.Input placeholder="Search IDEs..." bind:value={ideSearch} />
                  <Command.List class="max-h-60">
                    <Command.Empty>No IDE found.</Command.Empty>
                    <Command.Group>
                      {#each filteredIdes as ide (ide.value)}
                        <Command.Item
                          value={ide.value}
                          class="justify-start"
                          onSelect={() => { selectedIde = ide.value; ideComboOpen = false; ideSearch = "" }}
                        >
                          <Check class="mr-2 h-4 w-4 {selectedIde === ide.value ? 'opacity-100' : 'opacity-0'}" />
                          <img src={ide.icon} alt="" class="mr-2 h-4 w-4 shrink-0" />
                          {ide.label}
                        </Command.Item>
                      {/each}
                    </Command.Group>
                  </Command.List>
                </Command.Root>
              </Popover.Content>
            </Popover.Root>
          </div>

          <div class="flex justify-between gap-2 pt-2">
            <Button variant="outline" onclick={() => goToStep("source")}>Back</Button>
            <Button onclick={continueFromIde}>Continue</Button>
          </div>
        </div>

      {:else if currentStep === "review"}
        <div class="space-y-4">
          <div>
            <h2 class="text-lg font-semibold">Review</h2>
            <p class="text-sm text-muted-foreground">Confirm your workspace configuration before launching.</p>
          </div>

          <div class="space-y-1.5">
            <Label class="text-sm">Workspace Name</Label>
            <Input
              placeholder="Optional - derived from source if empty"
              value={workspaceName}
              oninput={(e) => (workspaceName = e.currentTarget.value)}
            />
            <p class="text-xs text-muted-foreground">
              Resolved id: <span class="font-mono">{resolvedId || "—"}</span>
            </p>
          </div>

          {#if resolvedIdInvalid}
            <Alert.Root>
              <TriangleAlert class="h-4 w-4 text-amber-600" />
              <Alert.Description class="text-amber-700 dark:text-amber-400">
                Please provide an explicit workspace name. The derived id is empty or invalid.
              </Alert.Description>
            </Alert.Root>
          {/if}

          {#if nameConflict}
            <Alert.Root>
              <TriangleAlert class="h-4 w-4 text-amber-600" />
              <Alert.Description class="text-amber-700 dark:text-amber-400">
                A workspace named "{resolvedId}" already exists. Choose a different name.
              </Alert.Description>
            </Alert.Root>
          {/if}

          <div class="rounded-lg border bg-card p-4 text-sm space-y-2">
            <div class="flex justify-between gap-3">
              <span class="text-muted-foreground">Provider</span>
              <span class="font-medium truncate">{selectedProvider}</span>
            </div>
            <div class="flex justify-between gap-3">
              <span class="text-muted-foreground">Source</span>
              <span class="font-medium truncate">{source}</span>
            </div>
            {#if workspaceFolder}
              <div class="flex justify-between gap-3">
                <span class="text-muted-foreground">Workspace Folder</span>
                <span class="font-medium truncate">{workspaceFolder}</span>
              </div>
            {/if}
            <div class="flex justify-between gap-3">
              <span class="text-muted-foreground">IDE</span>
              <span class="flex items-center gap-2 font-medium truncate">
                {#if ideIconSrc}
                  <img src={ideIconSrc} alt="" class="h-4 w-4 shrink-0" />
                {/if}
                <span class="truncate">{ideLabel}</span>
              </span>
            </div>
            <div class="flex justify-between gap-3">
              <span class="text-muted-foreground">Workspace ID</span>
              <span class="font-medium truncate font-mono">{resolvedId}</span>
            </div>
          </div>

          <div class="flex justify-between gap-2 pt-2">
            <Button variant="outline" onclick={() => goToStep("ide")}>Back</Button>
            <Button
              disabled={resolvedIdInvalid || nameConflict}
              onclick={continueFromReview}
            >
              Launch
            </Button>
          </div>
        </div>

      {:else if currentStep === "launch"}
        <div class="space-y-4">
          <div>
            <h2 class="text-lg font-semibold">
              {#if launchRunning}
                Creating Workspace
              {:else if launchSuccess}
                Workspace Ready
              {:else if launchError}
                Workspace Creation Failed
              {:else}
                Launching...
              {/if}
            </h2>
            <p class="text-sm text-muted-foreground">
              {#if launchRunning}
                Running workspace up...
              {:else if launchSuccess}
                {launchedWorkspaceId ?? resolvedId} is ready to use.
              {:else if launchError}
                Something went wrong while creating the workspace.
              {/if}
            </p>
          </div>

          {#if launchError}
            <Alert.Root variant="destructive">
              <AlertCircle class="h-4 w-4" />
              <Alert.Description>{launchError}</Alert.Description>
            </Alert.Root>
          {/if}

          {#if outputLines.length > 0}
            <div class="space-y-2">
              <div class="flex items-center justify-between">
                <h3 class="text-xs font-medium text-muted-foreground uppercase tracking-wider">Output</h3>
                <Button
                  variant="outline"
                  size="sm"
                  onclick={async () => {
                    try {
                      await navigator.clipboard.writeText(outputLines.map(stripAnsi).join("\n"))
                      toasts.success("Copied to clipboard")
                    } catch {
                      toasts.error("Failed to copy")
                    }
                  }}
                >
                  Copy
                </Button>
              </div>
              <div class="max-h-80 overflow-auto rounded-md border">
                <LogTable lines={outputLines} />
                <div bind:this={outputEl}></div>
              </div>
            </div>
          {:else if launchRunning}
            <div class="flex items-center justify-center py-8">
              <Loader2 class="h-6 w-6 animate-spin text-muted-foreground" />
            </div>
          {/if}

          <div class="flex justify-end gap-2 pt-2">
            {#if launchSuccess}
              <Button variant="outline" onclick={() => (open = false)}>Close</Button>
              <Button
                onclick={() => {
                  const id = launchedWorkspaceId ?? resolvedId
                  open = false
                  if (id) goto(`/workspaces/${id}`)
                }}
              >
                Open Workspace
              </Button>
            {:else if launchError}
              <Button variant="outline" onclick={() => (open = false)}>Close</Button>
              <Button onclick={handleLaunch}>Retry</Button>
            {/if}
          </div>
        </div>
      {/if}
    </div>
  </Dialog.Content>
</Dialog.Root>

<ConfirmDialog
  bind:open={confirmCancelOpen}
  title="Cancel workspace creation?"
  description="A workspace is currently being created. Closing this dialog will not stop the process, but you will lose visibility of the progress."
  confirmLabel="Close Anyway"
  variant="destructive"
  onconfirm={() => {
    open = false
    confirmCancelOpen = false
  }}
/>
