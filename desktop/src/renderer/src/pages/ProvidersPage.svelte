<script lang="ts">
import {
  AlertCircle,
  ArrowDownAZ,
  ChevronsUpDown,
  Hash,
  Plug,
  RefreshCw,
  SearchX,
} from "@lucide/svelte"
import { onMount } from "svelte"
import { goto } from "$lib/router.js"
import { Button } from "$lib/components/ui/button/index.js"
import * as DropdownMenu from "$lib/components/ui/dropdown-menu/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import CardSkeleton from "$lib/components/ui/skeleton/CardSkeleton.svelte"
import ProviderCard from "$lib/components/provider/ProviderCard.svelte"
import ProviderSheet from "$lib/components/provider/ProviderSheet.svelte"
import { providers, providersLoading } from "$lib/stores/providers.js"
import {
  loadCachedUpdates,
  providerVersions,
  refreshUpdates,
} from "$lib/stores/providerVersions.js"

let { params = {} }: { params?: Record<string, string> } = $props()

let search = $state("")
let sortBy = $state<"name" | "version">("name")

let activeId = $derived(decodeURIComponent(params.id ?? ""))
let activeProvider = $derived($providers.find((p) => p.name === activeId))
let sheetOpen = $derived(activeId !== "" && activeProvider !== undefined)

function openProvider(name: string) {
  goto(`/providers/${encodeURIComponent(name)}`)
}

function closeSheet() {
  if (activeId) goto("/providers")
}

onMount(async () => {
  await loadCachedUpdates().catch(() => undefined)
  await refreshUpdates().catch(() => undefined)
})

function checkedLabel(checkedAt: Date | null): string {
  if (!checkedAt) return "Updates not checked yet"
  return `Last checked ${checkedAt.toLocaleTimeString([], {
    hour: "numeric",
    minute: "2-digit",
  })}`
}

let filtered = $derived.by(() => {
  const q = search.toLowerCase()
  let list = $providers.filter((p) => {
    if (!q) return true
    return (
      p.name.toLowerCase().includes(q) ||
      (p.description ?? "").toLowerCase().includes(q) ||
      (p.version ?? "").toLowerCase().includes(q)
    )
  })

  if (sortBy === "version") {
    list = [...list].sort((a, b) =>
      (b.version ?? "").localeCompare(a.version ?? ""),
    )
  }

  return list
})
</script>

<div class="space-y-6">
  <div class="flex items-center justify-between">
    <h1 class="text-2xl font-bold">Providers</h1>
    <Button onclick={() => goto("/providers/add")}>Add Provider</Button>
  </div>

  <div class="flex flex-wrap items-center gap-2 rounded-md border bg-muted/20 px-3 py-2" role="status">
    <div class="min-w-0 flex-1 text-sm">
      <div class="text-muted-foreground">{checkedLabel($providerVersions.lastCheckedAt)}</div>
      {#if $providerVersions.refreshError}
        <div class="mt-0.5 flex items-center gap-1 text-destructive">
          <AlertCircle class="size-3.5 shrink-0" />
          <span>DevSy could not check for updates. Cached results may be out of date.</span>
        </div>
      {/if}
    </div>
    <Button
      variant="outline"
      size="sm"
      onclick={() => refreshUpdates().catch(() => undefined)}
      disabled={$providerVersions.refreshing}
    >
      <RefreshCw class={$providerVersions.refreshing ? "mr-2 size-4 animate-spin" : "mr-2 size-4"} />
      {$providerVersions.refreshing ? "Checking..." : "Check now"}
    </Button>
  </div>

  <div class="flex gap-2">
    <Input
      placeholder="Search by name, description, version..."
      value={search}
      oninput={(e) => (search = e.currentTarget.value)}
      class="flex-1"
    />
    <DropdownMenu.Root>
      <DropdownMenu.Trigger>
        {#snippet child({ props })}
          <Button variant="outline" class="w-36 justify-between" {...props}>
            {#if sortBy === "name"}
              <ArrowDownAZ class="mr-2 h-4 w-4" /> Name
            {:else}
              <Hash class="mr-2 h-4 w-4" /> Version
            {/if}
            <ChevronsUpDown class="ml-auto h-4 w-4 opacity-50" />
          </Button>
        {/snippet}
      </DropdownMenu.Trigger>
      <DropdownMenu.Content align="end">
        <DropdownMenu.RadioGroup bind:value={sortBy}>
          <DropdownMenu.RadioItem value="name">
            <ArrowDownAZ class="mr-2 h-4 w-4" /> Name
          </DropdownMenu.RadioItem>
          <DropdownMenu.RadioItem value="version">
            <Hash class="mr-2 h-4 w-4" /> Version
          </DropdownMenu.RadioItem>
        </DropdownMenu.RadioGroup>
      </DropdownMenu.Content>
    </DropdownMenu.Root>
  </div>

  {#if $providersLoading}
    <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
      {#each Array(3) as _}
        <CardSkeleton />
      {/each}
    </div>
  {:else if filtered.length === 0}
    <div class="flex flex-col items-center justify-center gap-4 py-16 text-center">
      {#if search}
        <SearchX class="h-10 w-10 text-muted-foreground" />
        <p class="text-muted-foreground">No providers match your search.</p>
      {:else}
        <Plug class="h-10 w-10 text-muted-foreground" />
        <p class="text-muted-foreground">No providers configured yet.</p>
        <Button onclick={() => goto("/providers/add")}>Add your first provider</Button>
      {/if}
    </div>
  {:else}
    <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
      {#each filtered as provider (provider.name)}
        <ProviderCard {provider} onopen={() => openProvider(provider.name)} />
      {/each}
    </div>
  {/if}
</div>

{#if activeProvider}
  <ProviderSheet
    provider={activeProvider}
    bind:open={
      () => sheetOpen,
      (v) => {
        if (!v) closeSheet()
      }
    }
    ondeleted={closeSheet}
  />
{/if}
