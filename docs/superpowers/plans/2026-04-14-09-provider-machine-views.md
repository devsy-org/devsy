# Plan 9: Provider & Machine Views

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build provider list/add/detail views with dynamic options forms, and machine list/detail views with status and actions.

**Architecture:** Provider and machine views follow the same pattern as workspaces: consume Svelte stores, call IPC commands for mutations. Provider options are dynamically rendered from the `ProviderOption` metadata returned by the daemon.

**Tech Stack:** SvelteKit, shadcn-svelte, Tailwind CSS v4, Svelte 5 runes

---

### Task 1: Create provider card component

**Files:**
- Create: `desktop-new/src/lib/components/provider/ProviderCard.svelte`

- [ ] **Step 1: Create ProviderCard.svelte**

```svelte
<script lang="ts">
  import { Card } from "$lib/components/ui/card";
  import { Button } from "$lib/components/ui/button";
  import { Badge } from "$lib/components/ui/badge";
  import type { Provider } from "$lib/types";
  import { providerDelete, providerUse } from "$lib/ipc/commands";
  import { goto } from "$app/navigation";

  interface Props {
    provider: Provider;
  }

  let { provider }: Props = $props();
  let loading = $state(false);

  async function handleSetDefault() {
    loading = true;
    try {
      await providerUse(provider.name);
    } finally {
      loading = false;
    }
  }

  async function handleDelete() {
    loading = true;
    try {
      await providerDelete(provider.name);
    } finally {
      loading = false;
    }
  }
</script>

<Card
  class="p-4 hover:bg-accent/30 transition-colors cursor-pointer"
  onclick={() => goto(`/providers/${provider.name}`)}
>
  <div class="flex items-start justify-between">
    <div class="flex-1 min-w-0">
      <div class="flex items-center gap-2">
        <h3 class="font-semibold text-foreground">{provider.name}</h3>
        {#if provider.isDefault}
          <Badge variant="default" class="text-xs">Default</Badge>
        {/if}
        {#if provider.version}
          <Badge variant="outline" class="text-xs">v{provider.version}</Badge>
        {/if}
      </div>
      {#if provider.description}
        <p class="text-sm text-muted-foreground mt-1 truncate">{provider.description}</p>
      {/if}
      {#if provider.source.github}
        <p class="text-xs text-muted-foreground mt-1">{provider.source.github}</p>
      {/if}
    </div>

    <div class="flex gap-1 ml-4" onclick={(e) => e.stopPropagation()}>
      {#if !provider.isDefault}
        <Button variant="ghost" size="sm" disabled={loading} onclick={handleSetDefault}>
          Set Default
        </Button>
      {/if}
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

- [ ] **Step 3: Commit**

```bash
git add desktop-new/src/lib/components/provider/
git commit -m "feat(ui): create ProviderCard component"
```

---

### Task 2: Build provider list view

**Files:**
- Modify: `desktop-new/src/routes/providers/+page.svelte`

- [ ] **Step 1: Replace stub with provider list**

```svelte
<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import ProviderCard from "$lib/components/provider/ProviderCard.svelte";
  import { providers } from "$lib/stores/providers";

  let search = $state("");

  let filtered = $derived(
    $providers.filter((p) =>
      p.name.toLowerCase().includes(search.toLowerCase())
    )
  );
</script>

<div class="flex items-center justify-between mb-6">
  <h2 class="text-2xl font-bold">Providers</h2>
  <Button href="/providers/add">Add Provider</Button>
</div>

<Input
  placeholder="Search providers..."
  bind:value={search}
  class="mb-4 max-w-sm"
/>

{#if filtered.length === 0}
  <div class="flex flex-col items-center justify-center py-12 text-muted-foreground">
    {#if $providers.length === 0}
      <p>No providers installed.</p>
      <Button variant="outline" class="mt-4" href="/providers/add">Add your first provider</Button>
    {:else}
      <p>No providers match "{search}"</p>
    {/if}
  </div>
{:else}
  <div class="flex flex-col gap-3">
    {#each filtered as provider (provider.name)}
      <ProviderCard {provider} />
    {/each}
  </div>
{/if}
```

- [ ] **Step 2: Verify build**

```bash
cd desktop-new
npm run build
```

- [ ] **Step 3: Commit**

```bash
git add desktop-new/src/routes/providers/+page.svelte
git commit -m "feat(ui): build provider list view"
```

---

### Task 3: Build provider add view

**Files:**
- Modify: `desktop-new/src/routes/providers/add/+page.svelte`

- [ ] **Step 1: Replace stub with add form**

```svelte
<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { Label } from "$lib/components/ui/label";
  import { Card } from "$lib/components/ui/card";
  import { providerAdd } from "$lib/ipc/commands";
  import { goto } from "$app/navigation";

  let providerName = $state("");
  let submitting = $state(false);
  let error = $state("");

  const suggestedProviders = [
    { name: "docker", description: "Docker provider" },
    { name: "ssh", description: "SSH provider" },
    { name: "kubernetes", description: "Kubernetes provider" },
    { name: "aws", description: "AWS provider" },
    { name: "gcloud", description: "Google Cloud provider" },
    { name: "azure", description: "Azure provider" },
    { name: "digitalocean", description: "DigitalOcean provider" },
  ];

  async function handleSubmit() {
    if (!providerName.trim()) {
      error = "Provider name or source is required";
      return;
    }

    submitting = true;
    error = "";

    try {
      await providerAdd(providerName.trim());
      goto("/providers");
    } catch (e) {
      error = String(e);
    } finally {
      submitting = false;
    }
  }

  function addSuggested(name: string) {
    providerName = name;
    handleSubmit();
  }
</script>

<div class="max-w-2xl">
  <div class="flex items-center justify-between mb-6">
    <h2 class="text-2xl font-bold">Add Provider</h2>
    <Button variant="ghost" href="/providers">Cancel</Button>
  </div>

  <Card class="p-6 mb-6">
    <form onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="flex flex-col gap-4">
      <div class="flex flex-col gap-2">
        <Label for="provider">Provider Name or Source</Label>
        <Input
          id="provider"
          placeholder="e.g., docker, ssh, or a GitHub URL"
          bind:value={providerName}
        />
        <p class="text-xs text-muted-foreground">
          Enter a built-in provider name or a GitHub URL for custom providers.
        </p>
      </div>

      {#if error}
        <p class="text-sm text-destructive">{error}</p>
      {/if}

      <div class="flex justify-end">
        <Button type="submit" disabled={submitting}>
          {submitting ? "Adding..." : "Add Provider"}
        </Button>
      </div>
    </form>
  </Card>

  <h3 class="text-lg font-semibold mb-3">Popular Providers</h3>
  <div class="grid grid-cols-2 gap-3">
    {#each suggestedProviders as sp (sp.name)}
      <Card class="p-3 hover:bg-accent/30 transition-colors cursor-pointer" onclick={() => addSuggested(sp.name)}>
        <h4 class="font-medium">{sp.name}</h4>
        <p class="text-xs text-muted-foreground">{sp.description}</p>
      </Card>
    {/each}
  </div>
</div>
```

- [ ] **Step 2: Verify build**

```bash
cd desktop-new
npm run build
```

- [ ] **Step 3: Commit**

```bash
git add desktop-new/src/routes/providers/add/
git commit -m "feat(ui): build provider add view with suggestions"
```

---

### Task 4: Build provider detail view with dynamic options

**Files:**
- Modify: `desktop-new/src/routes/providers/[id]/+page.svelte`

- [ ] **Step 1: Replace stub with detail view**

```svelte
<script lang="ts">
  import { page } from "$app/stores";
  import { Button } from "$lib/components/ui/button";
  import { Badge } from "$lib/components/ui/badge";
  import { Card } from "$lib/components/ui/card";
  import { Input } from "$lib/components/ui/input";
  import { Label } from "$lib/components/ui/label";
  import { Separator } from "$lib/components/ui/separator";
  import { providers } from "$lib/stores/providers";
  import {
    providerDelete,
    providerUse,
    providerUpdate,
    providerSetOptions,
  } from "$lib/ipc/commands";
  import { goto } from "$app/navigation";

  let id = $derived($page.params.id);
  let provider = $derived($providers.find((p) => p.name === id));
  let loading = $state(false);
  let optionValues = $state<Record<string, string>>({});
  let savingOptions = $state(false);

  // Initialize option values from provider
  $effect(() => {
    if (provider) {
      const vals: Record<string, string> = {};
      for (const [key, opt] of Object.entries(provider.options)) {
        vals[key] = opt.value || opt.default || "";
      }
      optionValues = vals;
    }
  });

  async function handleSetDefault() {
    loading = true;
    try {
      await providerUse(id);
    } finally {
      loading = false;
    }
  }

  async function handleUpdate() {
    loading = true;
    try {
      await providerUpdate(id);
    } finally {
      loading = false;
    }
  }

  async function handleDelete() {
    loading = true;
    try {
      await providerDelete(id);
      goto("/providers");
    } finally {
      loading = false;
    }
  }

  async function handleSaveOptions() {
    savingOptions = true;
    try {
      const opts = Object.entries(optionValues)
        .filter(([_, v]) => v.trim() !== "")
        .map(([k, v]) => `${k}=${v}`);
      await providerSetOptions(id, opts);
    } finally {
      savingOptions = false;
    }
  }
</script>

{#if !provider}
  <div class="flex flex-col items-center justify-center py-12">
    <p class="text-muted-foreground">Provider "{id}" not found.</p>
    <Button variant="outline" class="mt-4" href="/providers">Back to Providers</Button>
  </div>
{:else}
  <div class="flex items-center justify-between mb-6">
    <div class="flex items-center gap-3">
      <Button variant="ghost" href="/providers">Back</Button>
      <h2 class="text-2xl font-bold">{provider.name}</h2>
      {#if provider.isDefault}
        <Badge variant="default">Default</Badge>
      {/if}
      {#if provider.version}
        <Badge variant="outline">v{provider.version}</Badge>
      {/if}
    </div>
    <div class="flex gap-2">
      {#if !provider.isDefault}
        <Button variant="outline" size="sm" disabled={loading} onclick={handleSetDefault}>
          Set Default
        </Button>
      {/if}
      <Button variant="outline" size="sm" disabled={loading} onclick={handleUpdate}>
        Update
      </Button>
      <Button variant="destructive" size="sm" disabled={loading} onclick={handleDelete}>
        Delete
      </Button>
    </div>
  </div>

  <Card class="p-6 mb-6">
    <h3 class="text-lg font-semibold mb-2">Details</h3>
    <dl class="grid grid-cols-2 gap-4 text-sm">
      {#if provider.description}
        <div class="col-span-2">
          <dt class="text-muted-foreground">Description</dt>
          <dd class="mt-1">{provider.description}</dd>
        </div>
      {/if}
      {#if provider.source.github}
        <div>
          <dt class="text-muted-foreground">Source</dt>
          <dd class="mt-1 font-mono text-xs">{provider.source.github}</dd>
        </div>
      {/if}
    </dl>
  </Card>

  {#if Object.keys(provider.options).length > 0}
    <Card class="p-6">
      <h3 class="text-lg font-semibold mb-4">Options</h3>

      {#each provider.optionGroups as group (group.name)}
        <h4 class="text-sm font-medium text-muted-foreground mb-2 mt-4">{group.name}</h4>
        {#each group.options as optKey (optKey)}
          {#if provider.options[optKey]}
            {@const opt = provider.options[optKey]}
            <div class="flex flex-col gap-1 mb-3">
              <Label for={optKey}>
                {optKey}
                {#if opt.required}
                  <span class="text-destructive">*</span>
                {/if}
              </Label>
              <Input
                id={optKey}
                placeholder={opt.default || ""}
                bind:value={optionValues[optKey]}
              />
              {#if opt.description}
                <p class="text-xs text-muted-foreground">{opt.description}</p>
              {/if}
            </div>
          {/if}
        {/each}
      {/each}

      <!-- Options not in any group -->
      {#each Object.entries(provider.options) as [key, opt] (key)}
        {#if !provider.optionGroups.some((g) => g.options.includes(key))}
          <div class="flex flex-col gap-1 mb-3">
            <Label for={key}>
              {key}
              {#if opt.required}
                <span class="text-destructive">*</span>
              {/if}
            </Label>
            <Input
              id={key}
              placeholder={opt.default || ""}
              bind:value={optionValues[key]}
            />
            {#if opt.description}
              <p class="text-xs text-muted-foreground">{opt.description}</p>
            {/if}
          </div>
        {/if}
      {/each}

      <Separator class="my-4" />

      <div class="flex justify-end">
        <Button disabled={savingOptions} onclick={handleSaveOptions}>
          {savingOptions ? "Saving..." : "Save Options"}
        </Button>
      </div>
    </Card>
  {/if}
{/if}
```

- [ ] **Step 2: Verify build**

```bash
cd desktop-new
npm run build
```

- [ ] **Step 3: Commit**

```bash
git add desktop-new/src/routes/providers/
git commit -m "feat(ui): build provider detail view with dynamic options form"
```

---

### Task 5: Build machine list and detail views

**Files:**
- Create: `desktop-new/src/lib/components/machine/MachineCard.svelte`
- Modify: `desktop-new/src/routes/machines/+page.svelte`
- Modify: `desktop-new/src/routes/machines/[id]/+page.svelte`

- [ ] **Step 1: Create MachineCard.svelte**

```svelte
<script lang="ts">
  import { Card } from "$lib/components/ui/card";
  import { Button } from "$lib/components/ui/button";
  import { Badge } from "$lib/components/ui/badge";
  import type { Machine } from "$lib/types";
  import { machineDelete, machineStop } from "$lib/ipc/commands";
  import { goto } from "$app/navigation";

  interface Props {
    machine: Machine;
  }

  let { machine }: Props = $props();
  let loading = $state(false);

  async function handleStop() {
    loading = true;
    try {
      await machineStop(machine.id);
    } finally {
      loading = false;
    }
  }

  async function handleDelete() {
    loading = true;
    try {
      await machineDelete(machine.id);
    } finally {
      loading = false;
    }
  }
</script>

<Card
  class="p-4 hover:bg-accent/30 transition-colors cursor-pointer"
  onclick={() => goto(`/machines/${machine.id}`)}
>
  <div class="flex items-start justify-between">
    <div class="flex-1 min-w-0">
      <div class="flex items-center gap-2">
        <h3 class="font-semibold text-foreground">{machine.id}</h3>
        <Badge variant="outline" class="text-xs">{machine.provider.name}</Badge>
      </div>
      <p class="text-xs text-muted-foreground mt-1">
        Created: {machine.creationTimestamp}
      </p>
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

- [ ] **Step 2: Replace machine list stub**

`src/routes/machines/+page.svelte`:

```svelte
<script lang="ts">
  import { Input } from "$lib/components/ui/input";
  import MachineCard from "$lib/components/machine/MachineCard.svelte";
  import { machines } from "$lib/stores/machines";

  let search = $state("");

  let filtered = $derived(
    $machines.filter((m) =>
      m.id.toLowerCase().includes(search.toLowerCase())
    )
  );
</script>

<div class="flex items-center justify-between mb-6">
  <h2 class="text-2xl font-bold">Machines</h2>
</div>

<Input
  placeholder="Search machines..."
  bind:value={search}
  class="mb-4 max-w-sm"
/>

{#if filtered.length === 0}
  <div class="flex flex-col items-center justify-center py-12 text-muted-foreground">
    {#if $machines.length === 0}
      <p>No machines found.</p>
      <p class="text-xs mt-2">Machines are created automatically when workspaces use them.</p>
    {:else}
      <p>No machines match "{search}"</p>
    {/if}
  </div>
{:else}
  <div class="flex flex-col gap-3">
    {#each filtered as machine (machine.id)}
      <MachineCard {machine} />
    {/each}
  </div>
{/if}
```

- [ ] **Step 3: Replace machine detail stub**

`src/routes/machines/[id]/+page.svelte`:

```svelte
<script lang="ts">
  import { page } from "$app/stores";
  import { Button } from "$lib/components/ui/button";
  import { Badge } from "$lib/components/ui/badge";
  import { Card } from "$lib/components/ui/card";
  import { machines } from "$lib/stores/machines";
  import { machineDelete, machineStart, machineStop, machineStatus } from "$lib/ipc/commands";
  import { goto } from "$app/navigation";
  import { onMount } from "svelte";

  let id = $derived($page.params.id);
  let machine = $derived($machines.find((m) => m.id === id));
  let loading = $state(false);
  let status = $state("");

  onMount(async () => {
    try {
      status = await machineStatus(id);
    } catch {
      status = "unknown";
    }
  });

  async function handleStart() {
    loading = true;
    try {
      await machineStart(id);
      status = await machineStatus(id);
    } finally {
      loading = false;
    }
  }

  async function handleStop() {
    loading = true;
    try {
      await machineStop(id);
      status = await machineStatus(id);
    } finally {
      loading = false;
    }
  }

  async function handleDelete() {
    loading = true;
    try {
      await machineDelete(id);
      goto("/machines");
    } finally {
      loading = false;
    }
  }
</script>

{#if !machine}
  <div class="flex flex-col items-center justify-center py-12">
    <p class="text-muted-foreground">Machine "{id}" not found.</p>
    <Button variant="outline" class="mt-4" href="/machines">Back to Machines</Button>
  </div>
{:else}
  <div class="flex items-center justify-between mb-6">
    <div class="flex items-center gap-3">
      <Button variant="ghost" href="/machines">Back</Button>
      <h2 class="text-2xl font-bold">{machine.id}</h2>
      <Badge variant="outline">{machine.provider.name}</Badge>
    </div>
    <div class="flex gap-2">
      <Button variant="outline" size="sm" disabled={loading} onclick={handleStart}>
        Start
      </Button>
      <Button variant="outline" size="sm" disabled={loading} onclick={handleStop}>
        Stop
      </Button>
      <Button variant="destructive" size="sm" disabled={loading} onclick={handleDelete}>
        Delete
      </Button>
    </div>
  </div>

  <Card class="p-6">
    <dl class="grid grid-cols-2 gap-4 text-sm">
      <div>
        <dt class="text-muted-foreground">Provider</dt>
        <dd class="mt-1">{machine.provider.name}</dd>
      </div>
      <div>
        <dt class="text-muted-foreground">Status</dt>
        <dd class="mt-1">{status || "Loading..."}</dd>
      </div>
      <div>
        <dt class="text-muted-foreground">Created</dt>
        <dd class="mt-1">{machine.creationTimestamp}</dd>
      </div>
      <div>
        <dt class="text-muted-foreground">Context</dt>
        <dd class="mt-1">{machine.context || "default"}</dd>
      </div>
    </dl>
  </Card>
{/if}
```

- [ ] **Step 4: Verify build**

```bash
cd desktop-new
npm run build
```

- [ ] **Step 5: Commit**

```bash
git add desktop-new/src/lib/components/machine/ desktop-new/src/routes/machines/
git commit -m "feat(ui): build machine list and detail views"
```
