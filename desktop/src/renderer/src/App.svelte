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
import { initSecrets } from "$lib/stores/secrets.js"
import { initEnv } from "$lib/stores/env.js"
import {
  initSettings,
  syncAutoUpdateFromMain,
  autoUpdate,
} from "$lib/stores/settings.js"
import { terminalCount } from "$lib/stores/terminals.js"
import { togglePalette } from "$lib/stores/command-palette.js"
import { appReady, analyticsTrack } from "$lib/ipc/commands.js"
import { initSessionTracking } from "$lib/analytics.js"
import { location } from "$lib/router.js"
import UpdateBadge from "$lib/components/update/UpdateBadge.svelte"
import UpdateDialog from "$lib/components/update/UpdateDialog.svelte"
import {
  initUpdateStore,
  disposeUpdateStore,
} from "$lib/stores/updates.svelte.js"
import {
  initUpdateToasts,
  bindDialogOpener,
} from "$lib/components/update/update-toasts.js"

import DashboardPage from "./pages/DashboardPage.svelte"
import WorkspacesPage from "./pages/WorkspacesPage.svelte"
import WorkspaceDetailPage from "./pages/WorkspaceDetailPage.svelte"
import ProvidersPage from "./pages/ProvidersPage.svelte"
import ProviderAddPage from "./pages/ProviderAddPage.svelte"
import MachinesPage from "./pages/MachinesPage.svelte"
import MachineDetailPage from "./pages/MachineDetailPage.svelte"
import ContextsPage from "./pages/ContextsPage.svelte"
import SecretsPage from "./pages/SecretsPage.svelte"
import EnvPage from "./pages/EnvPage.svelte"
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
  "/providers/:id": ProvidersPage,
  "/machines": MachinesPage,
  "/machines/:id": MachineDetailPage,
  "/contexts": ContextsPage,
  "/secrets": SecretsPage,
  "/env": EnvPage,
  "/settings": SettingsPage,
  "/ssh-keys": SshKeysPage,
  "/terminals": TerminalsPage,
  "*": NotFoundPage,
}

let destroySettings: (() => void) | undefined

let updateDialogOpen = $state(false)
let unsubscribeToasts: (() => void) | null = null

const NAV_KEYS: Record<string, string> = {
  1: "/",
  2: "/workspaces",
  3: "/providers",
  4: "/machines",
  5: "/contexts",
  6: "/secrets",
  7: "/env",
  8: "/terminals",
  9: "/ssh-keys",
  0: "/settings",
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

let unsubLocation: (() => void) | undefined
let stopSessionTracking: (() => void) | undefined

function normalizeAnalyticsPath(path: string): string {
  // Match id segments but not the static sub-routes that share the prefix.
  if (path !== "/workspaces/new" && /^\/workspaces\/[^/]+$/.test(path))
    return "/workspaces/:id"
  if (path !== "/providers/add" && /^\/providers\/[^/]+$/.test(path))
    return "/providers/:id"
  if (/^\/machines\/[^/]+$/.test(path)) return "/machines/:id"
  return path
}

function screenName(path: string): string {
  const names: Record<string, string> = {
    "/": "dashboard",
    "/workspaces": "workspaces",
    "/workspaces/new": "workspace_new",
    "/workspaces/:id": "workspace_detail",
    "/providers": "providers",
    "/providers/add": "provider_add",
    "/providers/:id": "provider_detail",
    "/machines": "machines",
    "/machines/:id": "machine_detail",
    "/contexts": "contexts",
    "/secrets": "secrets",
    "/env": "env",
    "/settings": "settings",
    "/ssh-keys": "ssh_keys",
    "/terminals": "terminals",
  }
  return names[path] ?? path
}

onMount(async () => {
  initWorkspaces()
  initProviders()
  initMachines()
  initContexts()
  initSecrets()
  initEnv()
  destroySettings = initSettings()

  stopSessionTracking = initSessionTracking()

  unsubLocation = location.subscribe((path) => {
    const normalized = normalizeAnalyticsPath(path)
    analyticsTrack("page_view", {
      path: normalized,
      screen: screenName(normalized),
    })
  })

  // Signal the backend that the frontend is ready
  appReady().catch((err) => {
    console.warn("[Devsy] appReady failed:", err)
  })

  await initUpdateStore()
  await syncAutoUpdateFromMain()
  unsubscribeToasts = initUpdateToasts(() => {
    let value = true
    autoUpdate.subscribe((v) => (value = v))()
    return value
  })
  bindDialogOpener(() => (updateDialogOpen = true))
})

onDestroy(() => {
  unsubscribeToasts?.()
  disposeUpdateStore()
  stopSessionTracking?.()
  unsubLocation?.()
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
        <UpdateBadge onclick={() => (updateDialogOpen = true)} />
        <NotificationHistory />
        <ThemeSwitcher />
      </div>
    </header>

    <main class="flex min-h-0 flex-1 flex-col overflow-hidden p-6">
      <Router {routes} />
    </main>
  </SidebarUI.Inset>

  <Toaster richColors closeButton position="bottom-right" />
  <UpdateDialog bind:open={updateDialogOpen} autoDownloadEnabled={$autoUpdate} />
  <CommandPalette />
</SidebarUI.Provider>
