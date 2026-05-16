<script lang="ts">
import "./app.css"
import Router, { push } from "svelte-spa-router"
import { onMount, onDestroy } from "svelte"
import Sidebar from "$lib/components/layout/Sidebar.svelte"
import ThemeSwitcher from "$lib/components/layout/ThemeSwitcher.svelte"
import NotificationHistory from "$lib/components/layout/NotificationHistory.svelte"
import { Toaster } from "$lib/components/ui/sonner/index.js"
import CommandPalette from "$lib/components/layout/CommandPalette.svelte"
import Breadcrumbs from "$lib/components/layout/Breadcrumbs.svelte"
import * as SidebarUI from "$lib/components/ui/sidebar/index.js"
import { initWorkspaces, destroyWorkspaces } from "$lib/stores/workspaces.js"
import { initProviders, destroyProviders } from "$lib/stores/providers.js"
import { initMachines, destroyMachines } from "$lib/stores/machines.js"
import { initContexts, destroyContexts } from "$lib/stores/contexts.js"
import { initSettings } from "$lib/stores/settings.js"
import { terminalCount } from "$lib/stores/terminals.js"
import { togglePalette } from "$lib/stores/command-palette.js"
import { appReady } from "$lib/ipc/commands.js"

import DashboardPage from "./pages/DashboardPage.svelte"
import WorkspacesPage from "./pages/WorkspacesPage.svelte"
import WorkspaceDetailPage from "./pages/WorkspaceDetailPage.svelte"
import ProvidersPage from "./pages/ProvidersPage.svelte"
import ProviderAddPage from "./pages/ProviderAddPage.svelte"
import ProviderDetailPage from "./pages/ProviderDetailPage.svelte"
import MachinesPage from "./pages/MachinesPage.svelte"
import MachineDetailPage from "./pages/MachineDetailPage.svelte"
import ContextsPage from "./pages/ContextsPage.svelte"
import SettingsPage from "./pages/SettingsPage.svelte"
import SshKeysPage from "./pages/SshKeysPage.svelte"
import TerminalsPage from "./pages/TerminalsPage.svelte"
import NotFoundPage from "./pages/NotFoundPage.svelte"

const routes = {
  "/": DashboardPage,
  "/workspaces": WorkspacesPage,
  "/workspaces/new": WorkspacesPage,
  "/workspaces/:id": WorkspaceDetailPage,
  "/providers": ProvidersPage,
  "/providers/add": ProviderAddPage,
  "/providers/:id": ProviderDetailPage,
  "/machines": MachinesPage,
  "/machines/:id": MachineDetailPage,
  "/contexts": ContextsPage,
  "/settings": SettingsPage,
  "/ssh-keys": SshKeysPage,
  "/terminals": TerminalsPage,
  "*": NotFoundPage,
}

let destroySettings: (() => void) | undefined

const NAV_KEYS: Record<string, string> = {
  1: "/",
  2: "/workspaces",
  3: "/providers",
  4: "/machines",
  5: "/contexts",
  6: "/terminals",
  7: "/ssh-keys",
  8: "/settings",
}

function handleKeydown(e: KeyboardEvent) {
  if ((e.metaKey || e.ctrlKey) && e.key === "k") {
    e.preventDefault()
    togglePalette()
    return
  }
  if ((e.metaKey || e.ctrlKey) && e.key === "n") {
    e.preventDefault()
    push("/workspaces/new")
    return
  }
  if ((e.metaKey || e.ctrlKey) && NAV_KEYS[e.key]) {
    e.preventDefault()
    push(NAV_KEYS[e.key])
  }
}

onMount(() => {
  initWorkspaces()
  initProviders()
  initMachines()
  initContexts()
  destroySettings = initSettings()

  // Signal the backend that the frontend is ready
  appReady().catch((err) => {
    console.warn("[Devsy] appReady failed:", err)
  })
})

onDestroy(() => {
  destroyWorkspaces()
  destroyProviders()
  destroyMachines()
  destroyContexts()
  destroySettings?.()
})
</script>

<svelte:window onkeydown={handleKeydown} onpointerdown={() => window.focus()} />

<SidebarUI.Provider>
  <Sidebar terminalCount={$terminalCount} />

  <SidebarUI.Inset class="min-h-0 overflow-hidden">
    <header class="flex h-12 items-center justify-between border-b px-4">
      <div class="flex items-center gap-2">
        <SidebarUI.Trigger class="-ml-1" />
        <Breadcrumbs />
      </div>
      <div class="ml-auto flex items-center gap-1">
        <NotificationHistory />
        <ThemeSwitcher />
      </div>
    </header>

    <main class="flex min-h-0 flex-1 flex-col overflow-hidden p-6">
      <Router {routes} />
    </main>
  </SidebarUI.Inset>

  <Toaster richColors closeButton position="bottom-right" />
  <CommandPalette />
</SidebarUI.Provider>
