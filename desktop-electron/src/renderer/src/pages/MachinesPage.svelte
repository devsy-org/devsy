<script lang="ts">
import { ArrowDownAZ, Calendar, ChevronsUpDown, Server, SearchX } from "@lucide/svelte"
import { Button } from "$lib/components/ui/button/index.js"
import * as DropdownMenu from "$lib/components/ui/dropdown-menu/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import CardSkeleton from "$lib/components/ui/skeleton/CardSkeleton.svelte"
import MachineCard from "$lib/components/machine/MachineCard.svelte"
import { machines, machinesLoading } from "$lib/stores/machines.js"

let search = $state("")
let sortBy = $state<"name" | "created">("name")

let filtered = $derived.by(() => {
  const q = search.toLowerCase()
  let list = $machines.filter((m) => {
    if (!q) return true
    return (
      m.id.toLowerCase().includes(q) ||
      (m.provider?.name ?? "").toLowerCase().includes(q) ||
      (m.status ?? "").toLowerCase().includes(q)
    )
  })

  if (sortBy === "created") {
    list = [...list].sort((a, b) =>
      (b.created ?? "").localeCompare(a.created ?? ""),
    )
  }

  return list
})
</script>

<div class="space-y-6">
  <div class="flex items-center justify-between">
    <h1 class="text-2xl font-bold">Machines</h1>
  </div>

  <div class="flex gap-2">
    <Input
      placeholder="Search by name, provider, status..."
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
              <Calendar class="mr-2 h-4 w-4" /> Newest
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
          <DropdownMenu.RadioItem value="created">
            <Calendar class="mr-2 h-4 w-4" /> Newest
          </DropdownMenu.RadioItem>
        </DropdownMenu.RadioGroup>
      </DropdownMenu.Content>
    </DropdownMenu.Root>
  </div>

  {#if $machinesLoading}
    <div class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
      {#each Array(3) as _}
        <CardSkeleton />
      {/each}
    </div>
  {:else if filtered.length === 0}
    <div class="flex flex-col items-center justify-center gap-4 py-16 text-center">
      {#if search}
        <SearchX class="h-10 w-10 text-muted-foreground" />
        <p class="text-muted-foreground">No machines match your search.</p>
      {:else}
        <Server class="h-10 w-10 text-muted-foreground" />
        <p class="text-muted-foreground">No machines yet.</p>
        <p class="text-sm text-muted-foreground">
          Machines are created automatically when you start a workspace with a provider that supports them.
        </p>
      {/if}
    </div>
  {:else}
    <div class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
      {#each filtered as machine (machine.id)}
        <MachineCard {machine} />
      {/each}
    </div>
  {/if}
</div>
