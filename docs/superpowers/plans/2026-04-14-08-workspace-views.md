# Plan 8: Workspace Views

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the workspace list, create, and detail views with real-time status, action buttons, and log viewing.

**Architecture:** Views consume the `workspaces` Svelte store for reactive data. Action buttons call IPC commands. The detail view uses tabs for overview, logs (historical), and live output. All components use shadcn-svelte primitives.

**Tech Stack:** SvelteKit, shadcn-svelte, Tailwind CSS v4, Svelte 5 runes

---

### Task 1: Create workspace card component

**Files:**
- Create: `desktop-new/src/lib/components/workspace/WorkspaceCard.svelte`

- [ ] **Step 1: Create WorkspaceCard.svelte**

```svelte
<script lang="ts">
  import { Card } from "$lib/components/ui/card";
  import { Button } from "$lib/components/ui/button";
  import { Badge } from "$lib/components/ui/badge";
  import type { Workspace } from "$lib/types";
  import { workspaceStop, workspaceDelete } from "$lib/ipc/commands";
  import { goto } from "$app/navigation";

  interface Props {
    workspace: Workspace;
  }

  let { workspace }: Props = $props();

  let source = $derived(getSourceDisplay(workspace.source));
  let loading = $state(false);

  function getSourceDisplay(src: Workspace["source"]): string {
    if (src.gitRepository) return src.gitRepository;
    if (src.localFolder) return src.localFolder;
    if (src.image) return src.image;
    if (src.container) return src.container;
    return "Unknown";
  }

  async function handleStop() {
    loading = true;
    try {
      await workspaceStop(workspace.id);
    } catch (e) {
      console.error("Failed to stop workspace:", e);
    } finally {
      loading = false;
    }
  }

  async function handleDelete() {
    loading = true;
    try {
      await workspaceDelete(workspace.id);
    } catch (e) {
      console.error("Failed to delete workspace:", e);
    } finally {
      loading = false;
    }
  }
</script>

<Card class="p-4 hover:bg-accent/30 transition-colors cursor-pointer" onclick={() => goto(`/workspaces/${workspace.id}`)}>
  <div class="flex items-start justify-between">
    <div class="flex-1 min-w-0">
      <div class="flex items-center gap-2">
        <h3 class="font-semibold text-foreground truncate">{workspace.id}</h3>
        <Badge variant="outline" class="text-xs">{workspace.provider.name}</Badge>
        {#if workspace.ide.name}
          <Badge variant="secondary" class="text-xs">{workspace.ide.name}</Badge>
        {/if}
      </div>
      <p class="text-sm text-muted-foreground mt-1 truncate">{source}</p>
      <p class="text-xs text-muted-foreground mt-1">Last used: {workspace.lastUsed || "Never"}</p>
    </div>

    <div class="flex gap-1 ml-4" onclick={(e) => e.stopPropagation()}>
      <Button variant="ghost" size="sm" disabled={loading} onclick={handleStop}>
        Stop
      </Button>
      <Button variant="ghost" size="sm" disabled={loading} onclick={handleDelete}>
        Delete
      </Button>
    </div>
  </div>
</Card>
```

- [ ] **Step 2: Verify build**

```bash
cd desktop-new
npm run build
```

Expected: Build succeeds.

- [ ] **Step 3: Commit**

```bash
git add desktop-new/src/lib/components/workspace/
git commit -m "feat(ui): create WorkspaceCard component"
```

---

### Task 2: Build workspace list view

**Files:**
- Modify: `desktop-new/src/routes/workspaces/+page.svelte`

- [ ] **Step 1: Replace stub with full workspace list**

```svelte
<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import WorkspaceCard from "$lib/components/workspace/WorkspaceCard.svelte";
  import { workspaces } from "$lib/stores/workspaces";

  let search = $state("");

  let filtered = $derived(
    $workspaces.filter((ws) =>
      ws.id.toLowerCase().includes(search.toLowerCase())
    )
  );
</script>

<div class="flex items-center justify-between mb-6">
  <h2 class="text-2xl font-bold">Workspaces</h2>
  <Button href="/workspaces/new">Create Workspace</Button>
</div>

<Input
  placeholder="Search workspaces..."
  bind:value={search}
  class="mb-4 max-w-sm"
/>

{#if filtered.length === 0}
  <div class="flex flex-col items-center justify-center py-12 text-muted-foreground">
    {#if $workspaces.length === 0}
      <p>No workspaces yet.</p>
      <Button variant="outline" class="mt-4" href="/workspaces/new">Create your first workspace</Button>
    {:else}
      <p>No workspaces match "{search}"</p>
    {/if}
  </div>
{:else}
  <div class="flex flex-col gap-3">
    {#each filtered as workspace (workspace.id)}
      <WorkspaceCard {workspace} />
    {/each}
  </div>
{/if}
```

- [ ] **Step 2: Verify build**

```bash
cd desktop-new
npm run build
```

Expected: Build succeeds.

- [ ] **Step 3: Commit**

```bash
git add desktop-new/src/routes/workspaces/+page.svelte
git commit -m "feat(ui): build workspace list view with search and cards"
```

---

### Task 3: Build workspace create view

**Files:**
- Modify: `desktop-new/src/routes/workspaces/new/+page.svelte`

- [ ] **Step 1: Replace stub with create form**

```svelte
<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { Label } from "$lib/components/ui/label";
  import { Card } from "$lib/components/ui/card";
  import { providers } from "$lib/stores/providers";
  import { workspaceUp } from "$lib/ipc/commands";
  import { goto } from "$app/navigation";

  let source = $state("");
  let workspaceId = $state("");
  let selectedProvider = $state("");
  let selectedIde = $state("");
  let submitting = $state(false);
  let error = $state("");

  const ideOptions = [
    { value: "vscode", label: "VS Code" },
    { value: "openvscode", label: "OpenVSCode Server" },
    { value: "intellij", label: "IntelliJ" },
    { value: "goland", label: "GoLand" },
    { value: "pycharm", label: "PyCharm" },
    { value: "fleet", label: "Fleet" },
    { value: "jupyternotebook", label: "Jupyter Notebook" },
    { value: "cursor", label: "Cursor" },
    { value: "none", label: "None" },
  ];

  async function handleSubmit() {
    if (!source.trim()) {
      error = "Source is required";
      return;
    }

    submitting = true;
    error = "";

    try {
      await workspaceUp({
        source: source.trim(),
        workspaceId: workspaceId.trim() || undefined,
        provider: selectedProvider || undefined,
        ide: selectedIde || undefined,
      });
      goto("/workspaces");
    } catch (e) {
      error = String(e);
    } finally {
      submitting = false;
    }
  }
</script>

<div class="max-w-2xl">
  <div class="flex items-center justify-between mb-6">
    <h2 class="text-2xl font-bold">Create Workspace</h2>
    <Button variant="ghost" href="/workspaces">Cancel</Button>
  </div>

  <Card class="p-6">
    <form onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="flex flex-col gap-5">
      <div class="flex flex-col gap-2">
        <Label for="source">Source *</Label>
        <Input
          id="source"
          placeholder="Git repo URL, local path, or container image"
          bind:value={source}
        />
        <p class="text-xs text-muted-foreground">
          Examples: https://github.com/org/repo, ./local-folder, ubuntu:latest
        </p>
      </div>

      <div class="flex flex-col gap-2">
        <Label for="workspaceId">Workspace Name (optional)</Label>
        <Input
          id="workspaceId"
          placeholder="Auto-generated from source if empty"
          bind:value={workspaceId}
        />
      </div>

      <div class="flex flex-col gap-2">
        <Label for="provider">Provider</Label>
        <select
          id="provider"
          bind:value={selectedProvider}
          class="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
        >
          <option value="">Default</option>
          {#each $providers as provider (provider.name)}
            <option value={provider.name}>
              {provider.name}{provider.isDefault ? " (default)" : ""}
            </option>
          {/each}
        </select>
      </div>

      <div class="flex flex-col gap-2">
        <Label for="ide">IDE</Label>
        <select
          id="ide"
          bind:value={selectedIde}
          class="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
        >
          <option value="">Default</option>
          {#each ideOptions as ide (ide.value)}
            <option value={ide.value}>{ide.label}</option>
          {/each}
        </select>
      </div>

      {#if error}
        <p class="text-sm text-destructive">{error}</p>
      {/if}

      <div class="flex justify-end gap-2">
        <Button variant="outline" href="/workspaces">Cancel</Button>
        <Button type="submit" disabled={submitting}>
          {submitting ? "Creating..." : "Create Workspace"}
        </Button>
      </div>
    </form>
  </Card>
</div>
```

- [ ] **Step 2: Verify build**

```bash
cd desktop-new
npm run build
```

Expected: Build succeeds.

- [ ] **Step 3: Commit**

```bash
git add desktop-new/src/routes/workspaces/new/
git commit -m "feat(ui): build workspace create form"
```

---

### Task 4: Build workspace detail view with tabs

**Files:**
- Modify: `desktop-new/src/routes/workspaces/[id]/+page.svelte`

- [ ] **Step 1: Replace stub with tabbed detail view**

```svelte
<script lang="ts">
  import { page } from "$app/stores";
  import { Button } from "$lib/components/ui/button";
  import { Badge } from "$lib/components/ui/badge";
  import { Card } from "$lib/components/ui/card";
  import { Separator } from "$lib/components/ui/separator";
  import { Tabs, TabsContent, TabsList, TabsTrigger } from "$lib/components/ui/tabs";
  import { workspaces } from "$lib/stores/workspaces";
  import {
    workspaceStop,
    workspaceDelete,
    workspaceRebuild,
    workspaceLogs,
    workspaceLogContent,
  } from "$lib/ipc/commands";
  import { onCommandProgress } from "$lib/ipc/events";
  import { goto } from "$app/navigation";
  import { onMount, onDestroy } from "svelte";
  import type { LogEntry, CommandProgress } from "$lib/types";

  let id = $derived($page.params.id);
  let workspace = $derived($workspaces.find((w) => w.id === id));
  let loading = $state(false);
  let logs = $state<LogEntry[]>([]);
  let selectedLogContent = $state("");
  let liveOutput = $state<string[]>([]);

  let unlistenProgress: (() => void) | null = null;

  onMount(async () => {
    await loadLogs();

    const unlisten = await onCommandProgress((progress: CommandProgress) => {
      liveOutput = [...liveOutput, progress.output_line];
    });
    unlistenProgress = unlisten;
  });

  onDestroy(() => {
    if (unlistenProgress) unlistenProgress();
  });

  async function loadLogs() {
    try {
      logs = await workspaceLogs(id);
    } catch (e) {
      console.error("Failed to load logs:", e);
    }
  }

  async function viewLog(entry: LogEntry) {
    try {
      selectedLogContent = await workspaceLogContent(entry.file_path);
    } catch (e) {
      selectedLogContent = `Error loading log: ${e}`;
    }
  }

  async function handleStop() {
    loading = true;
    try {
      await workspaceStop(id);
    } finally {
      loading = false;
    }
  }

  async function handleDelete() {
    loading = true;
    try {
      await workspaceDelete(id);
      goto("/workspaces");
    } finally {
      loading = false;
    }
  }

  async function handleRebuild() {
    loading = true;
    liveOutput = [];
    try {
      await workspaceRebuild(id);
    } finally {
      loading = false;
    }
  }

  function getSourceDisplay(ws: typeof workspace): string {
    if (!ws) return "";
    const src = ws.source;
    if (src.gitRepository) return src.gitRepository;
    if (src.localFolder) return src.localFolder;
    if (src.image) return src.image;
    if (src.container) return src.container;
    return "Unknown";
  }
</script>

{#if !workspace}
  <div class="flex flex-col items-center justify-center py-12">
    <p class="text-muted-foreground">Workspace "{id}" not found.</p>
    <Button variant="outline" class="mt-4" href="/workspaces">Back to Workspaces</Button>
  </div>
{:else}
  <div class="flex items-center justify-between mb-6">
    <div class="flex items-center gap-3">
      <Button variant="ghost" href="/workspaces">Back</Button>
      <h2 class="text-2xl font-bold">{workspace.id}</h2>
      <Badge variant="outline">{workspace.provider.name}</Badge>
    </div>
    <div class="flex gap-2">
      <Button variant="outline" size="sm" disabled={loading} onclick={handleStop}>
        Stop
      </Button>
      <Button variant="outline" size="sm" disabled={loading} onclick={handleRebuild}>
        Rebuild
      </Button>
      <Button variant="destructive" size="sm" disabled={loading} onclick={handleDelete}>
        Delete
      </Button>
    </div>
  </div>

  <Tabs value="overview">
    <TabsList>
      <TabsTrigger value="overview">Overview</TabsTrigger>
      <TabsTrigger value="logs">Logs ({logs.length})</TabsTrigger>
      <TabsTrigger value="live">Live Output</TabsTrigger>
    </TabsList>

    <TabsContent value="overview" class="mt-4">
      <Card class="p-6">
        <dl class="grid grid-cols-2 gap-4 text-sm">
          <div>
            <dt class="text-muted-foreground">Source</dt>
            <dd class="font-mono mt-1">{getSourceDisplay(workspace)}</dd>
          </div>
          <div>
            <dt class="text-muted-foreground">Provider</dt>
            <dd class="mt-1">{workspace.provider.name}</dd>
          </div>
          <div>
            <dt class="text-muted-foreground">IDE</dt>
            <dd class="mt-1">{workspace.ide.name || "None"}</dd>
          </div>
          <div>
            <dt class="text-muted-foreground">Machine</dt>
            <dd class="mt-1">{workspace.machine.id || "Default"}</dd>
          </div>
          <div>
            <dt class="text-muted-foreground">Created</dt>
            <dd class="mt-1">{workspace.creationTimestamp}</dd>
          </div>
          <div>
            <dt class="text-muted-foreground">Last Used</dt>
            <dd class="mt-1">{workspace.lastUsed || "Never"}</dd>
          </div>
          <div>
            <dt class="text-muted-foreground">Context</dt>
            <dd class="mt-1">{workspace.context || "default"}</dd>
          </div>
          <div>
            <dt class="text-muted-foreground">UID</dt>
            <dd class="font-mono mt-1 text-xs">{workspace.uid}</dd>
          </div>
        </dl>
      </Card>
    </TabsContent>

    <TabsContent value="logs" class="mt-4">
      {#if logs.length === 0}
        <p class="text-muted-foreground">No logs recorded yet.</p>
      {:else}
        <div class="flex gap-4">
          <div class="w-64 flex flex-col gap-1">
            {#each logs as entry (entry.file_path)}
              <button
                class="text-left p-2 rounded text-sm hover:bg-accent/50 transition-colors"
                onclick={() => viewLog(entry)}
              >
                <div class="font-mono text-xs">{entry.timestamp}</div>
                <div class="text-muted-foreground">{entry.command}</div>
              </button>
            {/each}
          </div>
          <div class="flex-1">
            {#if selectedLogContent}
              <pre class="bg-muted p-4 rounded text-xs font-mono overflow-auto max-h-96 whitespace-pre-wrap">{selectedLogContent}</pre>
            {:else}
              <p class="text-muted-foreground">Select a log entry to view.</p>
            {/if}
          </div>
        </div>
      {/if}
    </TabsContent>

    <TabsContent value="live" class="mt-4">
      {#if liveOutput.length === 0}
        <p class="text-muted-foreground">No live output. Run an action to see output here.</p>
      {:else}
        <pre class="bg-muted p-4 rounded text-xs font-mono overflow-auto max-h-96 whitespace-pre-wrap">{liveOutput.join("\n")}</pre>
      {/if}
    </TabsContent>
  </Tabs>
{/if}
```

- [ ] **Step 2: Verify build**

```bash
cd desktop-new
npm run build
```

Expected: Build succeeds.

- [ ] **Step 3: Commit**

```bash
git add desktop-new/src/routes/workspaces/
git commit -m "feat(ui): build workspace detail view with overview, logs, and live output tabs"
```
