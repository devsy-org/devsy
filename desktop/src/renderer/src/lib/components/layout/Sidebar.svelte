<script lang="ts">
import {
  Box,
  Layers,
  LayoutDashboard,
  KeyRound,
  Plug,
  Search,
  Server,
  Settings,
  SquareTerminal,
} from "@lucide/svelte"
import AppLogo from "$lib/components/layout/AppLogo.svelte"
import { location } from "$lib/router.js"
import * as Sidebar from "$lib/components/ui/sidebar/index.js"
import { workspaces } from "$lib/stores/workspaces.js"
import { providers } from "$lib/stores/providers.js"
import { machines } from "$lib/stores/machines.js"
import { contexts } from "$lib/stores/contexts.js"
import { togglePalette } from "$lib/stores/command-palette.js"
import type { Component } from "svelte"

let { terminalCount = 0 }: { terminalCount?: number } = $props()

// Matches NAV_KEYS in App.svelte — maps shortcut number to route
const SHORTCUT_BY_HREF: Record<string, string> = {
  "/": "1",
  "/workspaces": "2",
  "/providers": "3",
  "/machines": "4",
  "/contexts": "5",
  "/terminals": "6",
  "/ssh-keys": "7",
  "/settings": "8",
}

const isMac =
  typeof navigator !== "undefined" &&
  /Mac|iPod|iPhone|iPad/.test(navigator.platform)
const modKey = isMac ? "⌘" : "Ctrl+"

interface NavItem {
  href: string
  label: string
  icon: Component
  badge?: number
  shortcut?: string
}

let mainNav: NavItem[] = $derived([
  {
    href: "/",
    label: "Dashboard",
    icon: LayoutDashboard,
    shortcut: SHORTCUT_BY_HREF["/"],
  },
  {
    href: "/workspaces",
    label: "Workspaces",
    icon: Box,
    badge: $workspaces.length,
    shortcut: SHORTCUT_BY_HREF["/workspaces"],
  },
  {
    href: "/providers",
    label: "Providers",
    icon: Plug,
    badge: $providers.length,
    shortcut: SHORTCUT_BY_HREF["/providers"],
  },
  {
    href: "/machines",
    label: "Machines",
    icon: Server,
    badge: $machines.length,
    shortcut: SHORTCUT_BY_HREF["/machines"],
  },
  {
    href: "/contexts",
    label: "Contexts",
    icon: Layers,
    badge: $contexts.length,
    shortcut: SHORTCUT_BY_HREF["/contexts"],
  },
  {
    href: "/terminals",
    label: "Terminals",
    icon: SquareTerminal,
    badge: terminalCount,
    shortcut: SHORTCUT_BY_HREF["/terminals"],
  },
  {
    href: "/ssh-keys",
    label: "SSH Keys",
    icon: KeyRound,
    shortcut: SHORTCUT_BY_HREF["/ssh-keys"],
  },
])

function isActive(href: string): boolean {
  return href === "/" ? $location === "/" : $location.startsWith(href)
}
</script>

<Sidebar.Root collapsible="icon">
  <Sidebar.Header>
    <Sidebar.Menu>
      <Sidebar.MenuItem>
        <Sidebar.MenuButton size="lg" class="pointer-events-none">
          <div class="flex aspect-square size-8 items-center justify-center overflow-hidden rounded-lg">
            <AppLogo class="size-full" />
          </div>
          <div class="grid flex-1 text-left text-sm leading-tight">
            <span class="truncate font-semibold">Devsy</span>
            <span class="truncate text-xs text-muted-foreground">Desktop</span>
          </div>
        </Sidebar.MenuButton>
      </Sidebar.MenuItem>
    </Sidebar.Menu>
  </Sidebar.Header>

  <Sidebar.Content>
    <Sidebar.Group>
      <Sidebar.GroupLabel>Navigation</Sidebar.GroupLabel>
      <Sidebar.GroupContent>
        <Sidebar.Menu>
          {#each mainNav as item (item.href)}
            {@const Icon = item.icon}
            <Sidebar.MenuItem>
              <Sidebar.MenuButton isActive={isActive(item.href)} tooltipContent={item.label}>
                {#snippet child({ props })}
                  <a href="#/{item.href === '/' ? '' : item.href.slice(1)}" {...props}>
                    <Icon />
                    <span>{item.label}</span>
                    {#if item.badge != null && item.badge > 0}
                      <span data-sidebar="menu-badge" class="ml-auto rounded-md bg-sidebar-accent px-1.5 text-xs font-medium tabular-nums text-sidebar-accent-foreground group-data-[collapsible=icon]:hidden">{item.badge}</span>
                    {/if}
                    {#if item.shortcut}
                      <kbd class="{item.badge ? '' : 'ml-auto '}text-[10px] text-muted-foreground/60 font-mono group-data-[collapsible=icon]:hidden">{modKey}{item.shortcut}</kbd>
                    {/if}
                  </a>
                {/snippet}
              </Sidebar.MenuButton>
            </Sidebar.MenuItem>
          {/each}
        </Sidebar.Menu>
      </Sidebar.GroupContent>
    </Sidebar.Group>
  </Sidebar.Content>

  <Sidebar.Footer>
    <Sidebar.Menu>
      <Sidebar.MenuItem>
        <Sidebar.MenuButton isActive={isActive("/settings")} tooltipContent="Settings">
          {#snippet child({ props })}
            <a href="#/settings" {...props}>
              <Settings />
              <span>Settings</span>
              <kbd class="ml-auto text-[10px] text-muted-foreground/60 font-mono group-data-[collapsible=icon]:hidden">{modKey}8</kbd>
            </a>
          {/snippet}
        </Sidebar.MenuButton>
      </Sidebar.MenuItem>
      <Sidebar.MenuItem>
        <Sidebar.MenuButton tooltipContent="Search ({modKey}K)" onclick={togglePalette}>
          <Search />
          <span>Search</span>
          <kbd class="ml-auto rounded border bg-muted px-1.5 py-0.5 text-xs font-mono group-data-[collapsible=icon]:hidden">{modKey}K</kbd>
        </Sidebar.MenuButton>
      </Sidebar.MenuItem>
    </Sidebar.Menu>
  </Sidebar.Footer>

  <Sidebar.Rail />
</Sidebar.Root>
