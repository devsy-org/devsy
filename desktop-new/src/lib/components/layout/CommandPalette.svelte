<script lang="ts">
import { goto } from "$app/navigation"
import { paletteOpen } from "$lib/stores/command-palette.js"
import { workspaces } from "$lib/stores/workspaces.js"
import { providers } from "$lib/stores/providers.js"
import { machines } from "$lib/stores/machines.js"
import type { PaletteItem } from "$lib/stores/command-palette.js"

let query = $state("")
let selectedIndex = $state(0)
let inputEl = $state<HTMLInputElement | null>(null)

// Build items list from navigation + dynamic resources
let allItems = $derived.by(() => {
  const items: PaletteItem[] = [
    {
      id: "nav-dashboard",
      label: "Dashboard",
      description: "Go to dashboard",
      href: "/",
    },
    {
      id: "nav-workspaces",
      label: "Workspaces",
      description: "View all workspaces",
      href: "/workspaces",
    },
    {
      id: "nav-new-workspace",
      label: "New Workspace",
      description: "Create a workspace",
      href: "/workspaces/new",
    },
    {
      id: "nav-providers",
      label: "Providers",
      description: "View all providers",
      href: "/providers",
    },
    {
      id: "nav-add-provider",
      label: "Add Provider",
      description: "Add a new provider",
      href: "/providers/add",
    },
    {
      id: "nav-machines",
      label: "Machines",
      description: "View all machines",
      href: "/machines",
    },
    {
      id: "nav-contexts",
      label: "Contexts",
      description: "Manage contexts",
      href: "/contexts",
    },
    {
      id: "nav-terminals",
      label: "Terminals",
      description: "Terminal sessions",
      href: "/terminals",
    },
    {
      id: "nav-settings",
      label: "Settings",
      description: "App settings",
      href: "/settings",
    },
  ]

  for (const ws of $workspaces) {
    items.push({
      id: `ws-${ws.id}`,
      label: ws.id,
      description: `Workspace · ${ws.provider?.name ?? ""}`,
      href: `/workspaces/${ws.id}`,
    })
  }

  for (const p of $providers) {
    items.push({
      id: `prov-${p.name}`,
      label: p.name,
      description: `Provider · ${p.version ?? ""}`,
      href: `/providers/${p.name}`,
    })
  }

  for (const m of $machines) {
    items.push({
      id: `mach-${m.id}`,
      label: m.id,
      description: `Machine · ${m.provider?.name ?? ""}`,
      href: `/machines/${m.id}`,
    })
  }

  return items
})

let filtered = $derived.by(() => {
  if (!query) return allItems.slice(0, 12)
  const q = query.toLowerCase()
  return allItems
    .filter(
      (item) =>
        item.label.toLowerCase().includes(q) ||
        (item.description ?? "").toLowerCase().includes(q),
    )
    .slice(0, 12)
})

function close() {
  paletteOpen.set(false)
  query = ""
  selectedIndex = 0
}

function select(item: PaletteItem) {
  if (item.href) goto(item.href)
  if (item.action) item.action()
  close()
}

function handleKeydown(e: KeyboardEvent) {
  if (e.key === "ArrowDown") {
    e.preventDefault()
    selectedIndex = Math.min(selectedIndex + 1, filtered.length - 1)
  } else if (e.key === "ArrowUp") {
    e.preventDefault()
    selectedIndex = Math.max(selectedIndex - 1, 0)
  } else if (e.key === "Enter" && filtered[selectedIndex]) {
    e.preventDefault()
    select(filtered[selectedIndex])
  } else if (e.key === "Escape") {
    e.preventDefault()
    close()
  }
}

// Reset selection when query changes
$effect(() => {
  query
  selectedIndex = 0
})

// Focus input when opened
$effect(() => {
  if ($paletteOpen) {
    // Use microtask to ensure DOM is rendered
    queueMicrotask(() => inputEl?.focus())
  }
})
</script>

{#if $paletteOpen}
  <div class="fixed inset-0 z-50 flex items-start justify-center pt-[20vh]">
    <!-- backdrop -->
    <button
      type="button"
      class="absolute inset-0 bg-background/80 backdrop-blur-sm"
      onclick={close}
      tabindex="-1"
      aria-label="Close command palette"
    ></button>

    <!-- palette -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions a11y_interactive_supports_focus -->
    <div
      class="relative w-full max-w-lg rounded-lg border bg-card shadow-2xl"
      role="dialog"
      aria-label="Command palette"
      onkeydown={handleKeydown}
    >
      <input
        bind:this={inputEl}
        type="text"
        placeholder="Type a command or search..."
        class="w-full rounded-t-lg border-b bg-transparent px-4 py-3 text-sm outline-none placeholder:text-muted-foreground"
        value={query}
        oninput={(e) => (query = e.currentTarget.value)}
      />

      <div class="max-h-72 overflow-y-auto p-1">
        {#if filtered.length === 0}
          <div class="px-4 py-6 text-center text-sm text-muted-foreground">
            No results found.
          </div>
        {:else}
          {#each filtered as item, i (item.id)}
            <button
              type="button"
              class="flex w-full items-center gap-3 rounded-md px-3 py-2 text-left text-sm transition-colors {i === selectedIndex ? 'bg-accent text-accent-foreground' : 'hover:bg-accent/50'}"
              onclick={() => select(item)}
              onmouseenter={() => (selectedIndex = i)}
            >
              <div class="min-w-0 flex-1">
                <div class="truncate font-medium">{item.label}</div>
                {#if item.description}
                  <div class="truncate text-xs text-muted-foreground">
                    {item.description}
                  </div>
                {/if}
              </div>
            </button>
          {/each}
        {/if}
      </div>

      <div class="flex items-center justify-between border-t px-4 py-2 text-xs text-muted-foreground">
        <span>Navigate with &uarr;&darr; · Enter to select · Esc to close</span>
      </div>
    </div>
  </div>
{/if}
