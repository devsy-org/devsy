<script lang="ts">
import { onMount, onDestroy } from "svelte"
import { MediaQuery } from "svelte/reactivity"
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
import * as Tabs from "$lib/components/ui/tabs/index.js"
import * as Select from "$lib/components/ui/select/index.js"
import { Progress } from "$lib/components/ui/progress/index.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import LanguageIcon from "$lib/components/workspace/LanguageIcon.svelte"
import ImagePicker from "$lib/components/workspace/ImagePicker.svelte"
import ConfirmDialog from "$lib/components/layout/ConfirmDialog.svelte"
import LogTable from "$lib/components/log/LogTable.svelte"
import { uniqueNamesGenerator, adjectives, animals } from "unique-names-generator"
import { workspaceUp, openDirectoryDialog } from "$lib/ipc/commands.js"
import { buildWorkspaceSource } from "$lib/utils/workspace-source.js"
import type {
  WorkspaceSourceType,
  GitRefType,
} from "$lib/utils/workspace-source.js"
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

const darkMode = new MediaQuery("(prefers-color-scheme: dark)")

const IDE_ICON_DARK_VARIANTS = new Set([
  "bob",
  "code-server",
  "cursor",
  "jupyter",
  "marimo",
  "none",
  "zed",
])

const ideIcon = (name: string) => {
  const variant = darkMode.current && IDE_ICON_DARK_VARIANTS.has(name) ? `${name}_dark` : name
  return `./icons/ides/${variant}.svg`
}

const IDE_GROUPS = [
  {
    label: "Primary",
    options: [
      { value: "none", label: "None", iconName: "none" },
      { value: "vscode", label: "VS Code", iconName: "vscode" },
      { value: "openvscode", label: "VS Code Browser", iconName: "vscodebrowser" },
      { value: "code-server", label: "code-server", iconName: "code-server" },
      { value: "cursor", label: "Cursor", iconName: "cursor" },
      { value: "zed", label: "Zed", iconName: "zed" },
      { value: "codium", label: "VSCodium", iconName: "codium" },
      { value: "windsurf", label: "Windsurf Editor", iconName: "windsurf" },
      { value: "antigravity", label: "Google Antigravity", iconName: "antigravity" },
      { value: "bob", label: "IBM Bob", iconName: "bob" },
    ],
  },
  {
    label: "JetBrains",
    options: [
      { value: "intellij", label: "IntelliJ IDEA", iconName: "intellij" },
      { value: "pycharm", label: "PyCharm", iconName: "pycharm" },
      { value: "phpstorm", label: "PhpStorm", iconName: "phpstorm" },
      { value: "rider", label: "Rider", iconName: "rider" },
      { value: "fleet", label: "Fleet", iconName: "fleet" },
      { value: "goland", label: "GoLand", iconName: "goland" },
      { value: "webstorm", label: "WebStorm", iconName: "webstorm" },
      { value: "rustrover", label: "RustRover", iconName: "rustrover" },
      { value: "rubymine", label: "RubyMine", iconName: "rubymine" },
      { value: "clion", label: "CLion", iconName: "clion" },
      { value: "dataspell", label: "DataSpell", iconName: "dataspell" },
    ],
  },
  {
    label: "Other",
    options: [
      { value: "jupyternotebook", label: "Jupyter Notebook", iconName: "jupyter" },
      { value: "marimo", label: "marimo", iconName: "marimo" },
      { value: "vscode-insiders", label: "VS Code Insiders", iconName: "vscode_insiders" },
      { value: "positron", label: "Positron", iconName: "positron" },
      { value: "rstudio", label: "RStudio Server", iconName: "rstudio" },
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
let selectedProvider = $state(
  $providers.find((p) => p.isDefault && p.state?.initialized)?.name ?? ""
)
let sourceType = $state<WorkspaceSourceType>("git")
let repoUrl = $state("")
let localPath = $state("")
let imageRef = $state("")
let refType = $state<GitRefType>("branch")
let refValue = $state("")
let subPath = $state("")
let devcontainerPath = $state("")
let prebuildRepository = $state("")
let workspaceFolder = $state("")
let advancedOpen = $state(false)
let selectedIde = $state("none")
let workspaceName = $state("")

let assembled = $derived(
  buildWorkspaceSource({
    sourceType,
    repoUrl,
    localPath,
    imageRef,
    refType,
    refValue,
    subPath,
    devcontainerPath,
    prebuildRepository,
  }),
)

let primarySourceValue = $derived(
  sourceType === "git"
    ? repoUrl.trim()
    : sourceType === "local"
      ? localPath.trim()
      : imageRef.trim(),
)

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
const ideIconSrc = $derived(selectedIdeEntry ? ideIcon(selectedIdeEntry.iconName) : undefined)

let filteredIdes = $derived(
  ideSearch
    ? ALL_IDES.filter((i) =>
        i.label.toLowerCase().includes(ideSearch.toLowerCase()),
      )
    : ALL_IDES,
)

let resolvedId = $derived(
  workspaceName.trim() ||
    assembled.source
      .trim()
      .split("/")
      .pop()
      ?.replace(/\.git$/, "")
      ?.replace(/@.*$/, "") ||
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
  selectedProvider = $providers.find((p) => p.isDefault && p.state?.initialized)?.name ?? ""
  sourceType = "git"
  repoUrl = ""
  localPath = ""
  imageRef = ""
  refType = "branch"
  refValue = ""
  subPath = ""
  devcontainerPath = ""
  prebuildRepository = ""
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

function randomName(): string {
  return uniqueNamesGenerator({
    dictionaries: [adjectives, animals],
    separator: "-",
    length: 2,
    style: "lowerCase",
  })
}

function uniquifyName(base: string): string {
  const existing = new Set($workspaces.map((ws) => ws.id.toLowerCase()))
  if (base && !existing.has(base.toLowerCase())) return base
  for (let i = 0; i < 50; i++) {
    const candidate = randomName()
    if (!existing.has(candidate.toLowerCase())) return candidate
  }
  return `${randomName()}-${Date.now().toString(36)}`
}

function continueFromProvider() {
  if (!selectedProvider) return
  currentStep = "source"
}

function continueFromSource() {
  if (!primarySourceValue) return
  if (!workspaceName.trim()) {
    const derived =
      assembled.source
        .trim()
        .split("/")
        .pop()
        ?.replace(/\.git$/, "")
        ?.replace(/@.*$/, "") ?? ""
    if (derived) workspaceName = uniquifyName(derived)
  } else {
    workspaceName = uniquifyName(workspaceName.trim())
  }
  currentStep = "ide"
}

async function handleBrowse() {
  const picked = await openDirectoryDialog()
  if (picked) localPath = picked
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
  repoUrl = t.source
  if (!workspaceName) {
    workspaceName = uniquifyName(t.name.toLowerCase().replace(/[^a-z0-9]/g, "-"))
  }
}
</script>

{#snippet advancedSection(isGit: boolean)}
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
      <div class="mt-2 space-y-3">
        {#if isGit}
          <div class="grid grid-cols-2 gap-3">
            <div class="space-y-1.5">
              <Label class="text-sm">Ref Type</Label>
              <Select.Root type="single" bind:value={refType}>
                <Select.Trigger class="w-full">
                  {refType === "branch" ? "Branch" : refType === "commit" ? "Commit" : "Pull Request"}
                </Select.Trigger>
                <Select.Content>
                  <Select.Item value="branch">Branch</Select.Item>
                  <Select.Item value="commit">Commit</Select.Item>
                  <Select.Item value="pr">Pull Request</Select.Item>
                </Select.Content>
              </Select.Root>
            </div>
            <div class="space-y-1.5">
              <Label class="text-sm">
                {refType === "branch" ? "Branch name" : refType === "commit" ? "Commit SHA" : "PR number"}
              </Label>
              <Input
                placeholder={refType === "branch" ? "main" : refType === "commit" ? "abc123…" : "42"}
                value={refValue}
                oninput={(e) => (refValue = e.currentTarget.value)}
              />
            </div>
          </div>
        {/if}

        <div class="space-y-1.5">
          <Label class="text-sm">Subfolder</Label>
          <Input
            placeholder="path/within/source (optional)"
            value={subPath}
            oninput={(e) => (subPath = e.currentTarget.value)}
          />
        </div>

        <div class="space-y-1.5">
          <Label class="text-sm">devcontainer.json path</Label>
          <Input
            placeholder=".devcontainer/devcontainer.json (optional)"
            value={devcontainerPath}
            oninput={(e) => (devcontainerPath = e.currentTarget.value)}
          />
        </div>

        <div class="space-y-1.5">
          <Label class="text-sm">Prebuild repository</Label>
          <Input
            placeholder="ghcr.io/org/prebuilds (optional)"
            value={prebuildRepository}
            oninput={(e) => (prebuildRepository = e.currentTarget.value)}
          />
        </div>
      </div>
    {/if}
  </div>
{/snippet}

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
                    <div class="flex items-center gap-2">
                      <div class="text-sm font-medium">{p.name}</div>
                      {#if p.isDefault}
                        <span class={badgeVariants({ variant: "default" })}>Default</span>
                      {/if}
                    </div>
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
            <p class="text-sm text-muted-foreground">
              Start from a Git repository, a local directory, or a container image.
            </p>
          </div>

          <Tabs.Root value={sourceType} onValueChange={(v) => (sourceType = v as WorkspaceSourceType)}>
            <Tabs.List class="grid w-full grid-cols-3">
              <Tabs.Trigger value="git">Git Repo</Tabs.Trigger>
              <Tabs.Trigger value="local">Local Directory</Tabs.Trigger>
              <Tabs.Trigger value="image">Image</Tabs.Trigger>
            </Tabs.List>

            <!-- GIT -->
            <Tabs.Content value="git" class="space-y-4 pt-2">
              <div class="space-y-2">
                <h3 class="text-xs font-medium uppercase tracking-wider text-muted-foreground">Quick Start Templates</h3>
                <div class="grid grid-cols-3 gap-2">
                  {#each TEMPLATES as template (template.name)}
                    <button
                      type="button"
                      class="flex flex-col items-center gap-1.5 rounded-lg border bg-card p-3 text-center text-sm transition-colors hover:bg-accent/50 active:scale-[0.98] {repoUrl === template.source ? 'border-primary ring-1 ring-primary' : ''}"
                      onclick={() => selectTemplate(template)}
                    >
                      <LanguageIcon name={template.name} class="h-10 w-10" />
                      <span class="truncate text-xs">{template.name}</span>
                    </button>
                  {/each}
                </div>
              </div>

              <div class="space-y-1.5">
                <Label class="text-sm">Repository URL *</Label>
                <Input
                  placeholder="github.com/org/repo"
                  value={repoUrl}
                  oninput={(e) => (repoUrl = e.currentTarget.value)}
                />
              </div>

              {@render advancedSection(true)}
            </Tabs.Content>

            <!-- LOCAL -->
            <Tabs.Content value="local" class="space-y-4 pt-2">
              <div class="space-y-1.5">
                <Label class="text-sm">Local Directory *</Label>
                <div class="flex gap-2">
                  <Input
                    placeholder="/path/to/project"
                    value={localPath}
                    oninput={(e) => (localPath = e.currentTarget.value)}
                  />
                  <Button variant="outline" onclick={handleBrowse}>Browse…</Button>
                </div>
                <p class="text-xs text-muted-foreground">
                  Local sources require a provider running on this machine.
                </p>
              </div>

              {@render advancedSection(false)}
            </Tabs.Content>

            <!-- IMAGE -->
            <Tabs.Content value="image" class="space-y-4 pt-2">
              <ImagePicker bind:value={imageRef} />
            </Tabs.Content>
          </Tabs.Root>

          <div class="flex justify-between gap-2 pt-2">
            <Button variant="outline" onclick={() => goToStep("provider")}>Back</Button>
            <Button disabled={!primarySourceValue} onclick={continueFromSource}>Continue</Button>
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
                          <img src={ideIcon(ide.iconName)} alt="" class="mr-2 h-4 w-4 shrink-0" />
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
              Auto-suggested to avoid conflicts. Edit to use a custom name.
            </p>
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
              <span class="text-muted-foreground">Source type</span>
              <span class="font-medium truncate capitalize">{sourceType}</span>
            </div>
            <div class="flex justify-between gap-3">
              <span class="text-muted-foreground">Source</span>
              <span class="font-medium truncate font-mono">{assembled.source}</span>
            </div>
            {#if sourceType === "git" && refValue.trim()}
              <div class="flex justify-between gap-3">
                <span class="text-muted-foreground">
                  {refType === "branch" ? "Branch" : refType === "commit" ? "Commit" : "Pull request"}
                </span>
                <span class="font-medium truncate">{refValue}</span>
              </div>
            {/if}
            {#if subPath.trim()}
              <div class="flex justify-between gap-3">
                <span class="text-muted-foreground">Subfolder</span>
                <span class="font-medium truncate">{subPath}</span>
              </div>
            {/if}
            {#if assembled.devcontainerPath}
              <div class="flex justify-between gap-3">
                <span class="text-muted-foreground">devcontainer.json</span>
                <span class="font-medium truncate">{assembled.devcontainerPath}</span>
              </div>
            {/if}
            {#if assembled.prebuildRepository}
              <div class="flex justify-between gap-3">
                <span class="text-muted-foreground">Prebuild repo</span>
                <span class="font-medium truncate">{assembled.prebuildRepository}</span>
              </div>
            {/if}
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
