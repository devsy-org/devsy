<script lang="ts">
import { goto } from "$app/navigation"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { Label } from "$lib/components/ui/label/index.js"
import * as Select from "$lib/components/ui/select/index.js"
import { workspaceUp } from "$lib/ipc/commands.js"
import { providers } from "$lib/stores/providers.js"

const IDE_OPTIONS = [
  { value: "vscode", label: "VS Code" },
  { value: "openvscode", label: "OpenVSCode" },
  { value: "intellij", label: "IntelliJ" },
  { value: "goland", label: "GoLand" },
  { value: "pycharm", label: "PyCharm" },
  { value: "fleet", label: "Fleet" },
  { value: "jupyternotebook", label: "Jupyter Notebook" },
  { value: "none", label: "None" },
]

let source = $state("")
let name = $state("")
let selectedProvider = $state<string | undefined>(undefined)
let selectedIde = $state<string | undefined>(undefined)
let error = $state("")
let submitting = $state(false)

async function handleSubmit() {
  if (!source.trim()) {
    error = "Source is required"
    return
  }

  error = ""
  submitting = true

  try {
    const workspaceId =
      name.trim() ||
      source
        .trim()
        .split("/")
        .pop()
        ?.replace(/\.git$/, "") ||
      undefined

    await workspaceUp({
      source: source.trim(),
      workspaceId,
      provider: selectedProvider,
      ide: selectedIde,
    })
    goto("/workspaces")
  } catch (err) {
    error = err instanceof Error ? err.message : String(err)
  } finally {
    submitting = false
  }
}
</script>

<div class="mx-auto max-w-xl space-y-6">
  <div class="flex items-center gap-4">
    <Button variant="ghost" size="sm" onclick={() => goto("/workspaces")}>
      &larr; Back
    </Button>
    <h1 class="text-2xl font-bold">Create Workspace</h1>
  </div>

  {#if error}
    <div class="rounded-md border border-destructive bg-destructive/10 p-3 text-sm text-destructive">
      {error}
    </div>
  {/if}

  <form class="space-y-4" onsubmit={(e) => { e.preventDefault(); handleSubmit() }}>
    <div class="space-y-2">
      <Label>Source *</Label>
      <Input
        placeholder="github.com/org/repo, local path, or image"
        value={source}
        oninput={(e) => (source = e.currentTarget.value)}
      />
    </div>

    <div class="space-y-2">
      <Label>Workspace Name</Label>
      <Input
        placeholder="Optional - derived from source if empty"
        value={name}
        oninput={(e) => (name = e.currentTarget.value)}
      />
    </div>

    <div class="space-y-2">
      <Label>Provider</Label>
      <Select.Root type="single" onValueChange={(v) => (selectedProvider = v)}>
        <Select.Trigger>
          <Select.Value placeholder="Select a provider" />
        </Select.Trigger>
        <Select.Content>
          {#each $providers as p (p.name)}
            <Select.Item value={p.name} label={p.name} />
          {/each}
        </Select.Content>
      </Select.Root>
    </div>

    <div class="space-y-2">
      <Label>IDE</Label>
      <Select.Root type="single" onValueChange={(v) => (selectedIde = v)}>
        <Select.Trigger>
          <Select.Value placeholder="Select an IDE" />
        </Select.Trigger>
        <Select.Content>
          {#each IDE_OPTIONS as ide (ide.value)}
            <Select.Item value={ide.value} label={ide.label} />
          {/each}
        </Select.Content>
      </Select.Root>
    </div>

    <Button type="submit" disabled={submitting} class="w-full">
      {submitting ? "Creating..." : "Create Workspace"}
    </Button>
  </form>
</div>
