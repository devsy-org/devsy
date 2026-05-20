<script lang="ts">
import { onDestroy } from "svelte"
import { goto } from "$lib/router.js"
import { Check, ChevronsUpDown, TriangleAlert } from "@lucide/svelte"
import { Button } from "$lib/components/ui/button/index.js"
import * as Command from "$lib/components/ui/command/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { Label } from "$lib/components/ui/label/index.js"
import * as Popover from "$lib/components/ui/popover/index.js"
import * as Sheet from "$lib/components/ui/sheet/index.js"
import { Spinner } from "$lib/components/ui/spinner/index.js"
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
import { stripAnsi } from "$lib/utils/log-parser.js"
import type { UnlistenFn } from "$lib/ipc/types.js"

let {
  open = $bindable(false),
}: {
  open: boolean
} = $props()

const IDE_GROUPS = [
  {
    label: "Primary",
    options: [
      { value: "none", label: "None" },
      { value: "vscode", label: "VS Code" },
      { value: "openvscode", label: "VS Code Browser" },
      { value: "cursor", label: "Cursor" },
      { value: "zed", label: "Zed" },
      { value: "codium", label: "VSCodium" },
      { value: "windsurf", label: "Windsurf Editor" },
      { value: "antigravity", label: "Google Antigravity" },
      { value: "bob", label: "IBM Bob" },
    ],
  },
  {
    label: "JetBrains",
    options: [
      { value: "intellij", label: "IntelliJ IDEA" },
      { value: "pycharm", label: "PyCharm" },
      { value: "phpstorm", label: "PhpStorm" },
      { value: "rider", label: "Rider" },
      { value: "fleet", label: "Fleet" },
      { value: "goland", label: "GoLand" },
      { value: "webstorm", label: "WebStorm" },
      { value: "rustrover", label: "RustRover" },
      { value: "rubymine", label: "RubyMine" },
      { value: "clion", label: "CLion" },
      { value: "dataspell", label: "DataSpell" },
    ],
  },
  {
    label: "Other",
    options: [
      { value: "jupyternotebook", label: "Jupyter Notebook" },
      { value: "vscode-insiders", label: "VS Code Insiders" },
      { value: "positron", label: "Positron" },
      { value: "rstudio", label: "RStudio Server" },
    ],
  },
]

const ALL_IDES = IDE_GROUPS.flatMap((g) => g.options)

const TEMPLATES = [
  {
    name: "Python",
    source: "https://github.com/microsoft/vscode-remote-try-python",
  },
  {
    name: "Node.js",
    source: "https://github.com/microsoft/vscode-remote-try-node",
  },
  {
    name: "Go",
    source: "https://github.com/microsoft/vscode-remote-try-go",
  },
  {
    name: "Rust",
    source: "https://github.com/microsoft/vscode-remote-try-rust",
  },
  {
    name: "Java",
    source: "https://github.com/microsoft/vscode-remote-try-java",
  },
  {
    name: "PHP",
    source: "https://github.com/microsoft/vscode-remote-try-php",
  },
  {
    name: "C++",
    source: "https://github.com/microsoft/vscode-remote-try-cpp",
  },
  {
    name: ".NET",
    source: "https://github.com/microsoft/vscode-remote-try-dotnet",
  },
  {
    name: "Ruby",
    source: "https://github.com/skevetter/devsy-quickstart-ruby",
  },
]

let source = $state("")
let name = $state("")
let selectedProvider = $state("")
let selectedIde = $state("")

let providerComboOpen = $state(false)
let providerSearch = $state("")
let ideComboOpen = $state(false)
let ideSearch = $state("")

const providerLabel = $derived(selectedProvider || "Select a provider...")
const ideLabel = $derived(
  ALL_IDES.find((i) => i.value === selectedIde)?.label ?? "Select an IDE...",
)

let filteredProviders = $derived(
  providerSearch
    ? $providers.filter((p) =>
        p.name.toLowerCase().includes(providerSearch.toLowerCase()),
      )
    : $providers,
)

let filteredIdes = $derived(
  ideSearch
    ? ALL_IDES.filter((i) =>
        i.label.toLowerCase().includes(ideSearch.toLowerCase()),
      )
    : ALL_IDES,
)

$effect(() => {
  if (!selectedProvider && $providers.length > 0) {
    const initialized = $providers.filter((p) => p.state?.initialized === true)
    if (initialized.length === 1) {
      selectedProvider = initialized[0].name
    } else if (initialized.length === 0 && $providers.length === 1) {
      selectedProvider = $providers[0].name
    }
  }
})

// Reset form when sheet opens
$effect(() => {
  if (open) {
    source = ""
    name = ""
    selectedProvider = ""
    selectedIde = ""
    error = ""
    outputLines = []
    commandId = null
    submitting = false
    createdId = null
  }
})

let confirmCancelOpen = $state(false)

let error = $state("")
let submitting = $state(false)
let createdId = $state<string | null>(null)

let commandId = $state<string | null>(null)
let outputLines = $state<string[]>([])
let outputEl = $state<HTMLDivElement | null>(null)
let openBtnEl = $state<HTMLButtonElement | null>(null)
let unlisten: UnlistenFn | null = null

onDestroy(() => {
  unlisten?.()
})

function handleProgress(progress: CommandProgress, wsId: string | undefined) {
  if (progress.message) {
    outputLines = [...outputLines, progress.message]
    requestAnimationFrame(() => {
      outputEl?.scrollIntoView({ block: "end", behavior: "smooth" })
    })
  }
  if (progress.done) {
    submitting = false
    if (stripAnsi(progress.message).includes("Exit code: 0")) {
      createdId = wsId ?? null
      toasts.success(`Workspace ${wsId ?? "created"} is ready`)
      requestAnimationFrame(() => {
        openBtnEl?.scrollIntoView({ block: "center", behavior: "smooth" })
      })
    } else {
      toasts.error("Workspace creation failed. Check output for details.")
    }
  }
}

function handleOpenChange(newOpen: boolean) {
  if (!newOpen && submitting) {
    confirmCancelOpen = true
    return
  }
  open = newOpen
}

async function handleSubmit() {
  if (!source.trim()) {
    error = "Source is required"
    return
  }

  const workspaceId =
    name.trim() ||
    source
      .trim()
      .split("/")
      .pop()
      ?.replace(/\.git$/, "") ||
    undefined

  if (
    workspaceId &&
    $workspaces.some((ws) => ws.id.toLowerCase() === workspaceId.toLowerCase())
  ) {
    error = `Workspace "${workspaceId}" already exists. Choose a different name.`
    return
  }

  error = ""
  submitting = true
  outputLines = []

  try {
    // Buffer events arriving before commandId is known to avoid the race
    // where streaming events arrive before workspaceUp() resolves.
    const pendingEvents: CommandProgress[] = []
    let resolvedCommandId: string | null = null

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
      ide: selectedIde || undefined,
      debug: true,
    })

    commandId = cmdId
    resolvedCommandId = cmdId

    // Replay any events that arrived before the commandId was known
    for (const event of pendingEvents) {
      if (event.commandId === cmdId) {
        handleProgress(event, workspaceId)
      }
    }
  } catch (err) {
    toasts.error(`Failed to create workspace: ${extractErrorMessage(err)}`)
    submitting = false
  }
}
</script>

<Sheet.Root open={open} onOpenChange={handleOpenChange}>
  <Sheet.ResizableContent>
    <Sheet.Header class="p-6">
      <Sheet.Title>Create Workspace</Sheet.Title>
      <Sheet.Description>Set up a new development workspace from a source repository, image, or local path.</Sheet.Description>
    </Sheet.Header>

    <div class="flex-1 overflow-y-auto space-y-4 px-6 pb-16">
      <div class="space-y-3">
        <h3 class="text-xs font-medium text-muted-foreground uppercase tracking-wider">Quick Start Templates</h3>
        <div class="grid grid-cols-3 gap-2">
          {#each TEMPLATES as template (template.name)}
            <button
              type="button"
              class="flex flex-col items-center gap-1.5 rounded-lg border bg-card p-3 text-center text-sm transition-colors hover:bg-accent/50 active:scale-[0.98] {source === template.source ? 'border-primary ring-1 ring-primary' : ''}"
              onclick={() => { source = template.source; name = template.name.toLowerCase().replace(/[^a-z0-9]/g, '-') }}
              disabled={submitting}
            >
              <LanguageIcon name={template.name} class="h-10 w-10" />
              <span class="truncate text-xs">{template.name}</span>
            </button>
          {/each}
        </div>
      </div>

      {#if error}
        <div class="rounded-md border border-destructive bg-destructive/10 p-3 text-sm text-destructive">
          {error}
        </div>
      {/if}

      {#if $providers.length === 0}
        <div class="flex items-start gap-3 rounded-md border border-destructive bg-destructive/10 p-3 text-sm text-destructive">
          <TriangleAlert class="mt-0.5 h-4 w-4 shrink-0" />
          <div class="flex flex-col gap-2">
            <span>No providers configured. You need at least one provider to create a workspace.</span>
            <Button variant="outline" size="sm" onclick={() => { open = false; goto('/providers/add') }}>
              Add Provider
            </Button>
          </div>
        </div>
      {/if}

      <form class="space-y-4" onsubmit={(e) => { e.preventDefault(); handleSubmit() }}>
        <div class="space-y-1.5">
          <Label class="text-sm">Source *</Label>
          <Input
            placeholder="github.com/org/repo, local path, or image"
            value={source}
            oninput={(e) => (source = e.currentTarget.value)}
            disabled={submitting}
            class="h-9"
          />
        </div>

        <div class="space-y-1.5">
          <Label class="text-sm">Workspace Name</Label>
          <Input
            placeholder="Optional - derived from source if empty"
            value={name}
            oninput={(e) => (name = e.currentTarget.value)}
            disabled={submitting}
            class="h-9"
          />
        </div>

        <div class="space-y-1.5">
          <Label class="text-sm">Provider</Label>
          <Popover.Root bind:open={providerComboOpen}>
            <Popover.Trigger class="w-full">
              {#snippet child({ props })}
                <Button variant="outline" class="h-9 w-full justify-between" {...props} disabled={submitting}>
                  <span class="flex-1 truncate text-left">{providerLabel}</span>
                  <ChevronsUpDown class="ml-2 h-4 w-4 shrink-0 opacity-50" />
                </Button>
              {/snippet}
            </Popover.Trigger>
            <Popover.Content class="w-[var(--bits-popover-anchor-width)] p-0" align="start">
              <Command.Root shouldFilter={false}>
                <Command.Input placeholder="Search providers..." bind:value={providerSearch} />
                <Command.List class="max-h-60">
                  <Command.Empty>No provider found.</Command.Empty>
                  <Command.Group>
                    {#each filteredProviders as p (p.name)}
                      <Command.Item
                        value={p.name}
                        class="justify-start"
                        onSelect={() => { selectedProvider = p.name; providerComboOpen = false; providerSearch = "" }}
                      >
                        <Check class="mr-2 h-4 w-4 {selectedProvider === p.name ? 'opacity-100' : 'opacity-0'}" />
                        {p.name}
                        {#if p.state?.initialized !== true}
                          <span class="ml-auto text-xs text-destructive">(not initialized)</span>
                        {/if}
                      </Command.Item>
                    {/each}
                  </Command.Group>
                  <Command.Separator />
                  <Command.Item
                    value="__add_provider__"
                    class="justify-start text-muted-foreground"
                    onSelect={() => { providerComboOpen = false; providerSearch = ""; open = false; goto('/providers/add') }}
                  >
                    + Add Provider
                  </Command.Item>
                </Command.List>
              </Command.Root>
            </Popover.Content>
          </Popover.Root>

          {#if selectedProvider && $providers.find(p => p.name === selectedProvider)?.state?.initialized !== true}
            <div class="flex items-start gap-2 rounded-md border border-amber-500/50 bg-amber-500/10 p-3 text-sm">
              <TriangleAlert class="mt-0.5 h-4 w-4 shrink-0 text-amber-600" />
              <div>
                <p class="font-medium text-amber-700 dark:text-amber-400">Provider not initialized</p>
                <p class="text-amber-600 dark:text-amber-400/80">
                  This provider needs to be initialized before creating workspaces.
                  <button
                    type="button"
                    class="underline hover:no-underline"
                    onclick={() => { open = false; goto("/providers/" + selectedProvider + "?setup=true") }}
                  >
                    Initialize it now
                  </button>
                </p>
              </div>
            </div>
          {/if}
        </div>

        <div class="space-y-1.5">
          <Label class="text-sm">IDE</Label>
          <Popover.Root bind:open={ideComboOpen}>
            <Popover.Trigger class="w-full">
              {#snippet child({ props })}
                <Button variant="outline" class="h-9 w-full justify-between" {...props} disabled={submitting}>
                  <span class="flex-1 truncate text-left">{ideLabel}</span>
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
                        {ide.label}
                      </Command.Item>
                    {/each}
                  </Command.Group>
                </Command.List>
              </Command.Root>
            </Popover.Content>
          </Popover.Root>
        </div>

        <Sheet.Footer class="px-0 pt-2">
          <Button type="submit" disabled={submitting || $providers.length === 0} class="w-full">
            {#if submitting}<Spinner />{/if}
            {submitting ? "Creating..." : "Create Workspace"}
          </Button>
        </Sheet.Footer>
      </form>

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
          <div class="max-h-96 overflow-auto rounded-md border">
            <LogTable lines={outputLines} />
            <div bind:this={outputEl}></div>
          </div>
        </div>
      {/if}

      {#if createdId}
        <div class="pb-8">
          <Button class="w-full" bind:ref={openBtnEl} onclick={() => { open = false; goto(`/workspaces/${createdId}`) }}>
            Open Workspace
          </Button>
        </div>
      {/if}
    </div>
  </Sheet.ResizableContent>
</Sheet.Root>

<ConfirmDialog
  bind:open={confirmCancelOpen}
  title="Cancel workspace creation?"
  description="A workspace is currently being created. Closing this sheet will not stop the process, but you will lose visibility of the progress."
  confirmLabel="Close Anyway"
  variant="destructive"
  onconfirm={() => { open = false; confirmCancelOpen = false }}
/>
