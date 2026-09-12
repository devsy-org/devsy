import { join } from "node:path"
import { app, Menu, nativeImage, nativeTheme, Tray } from "electron"
import type { DaemonState, Workspace } from "./state.js"
import {
  getLastStatus,
  installUpdate,
  onUpdateStatusChanged,
  type UpdateStatus,
} from "./updater.js"
import { isActiveWorkspaceStatus } from "./workspace-status.js"

export function buildUpdateMenuItems(
  status: UpdateStatus,
  onInstall: () => void,
): Electron.MenuItemConstructorOptions[] {
  const installationFailed =
    status.state === "error" && status.code === "install-failed"
  if (status.state !== "downloaded" && !installationFailed) return []
  const label = installationFailed
    ? `Retry Install Update v${status.version ?? ""}`
    : `Install Update v${status.version ?? ""}`
  return [
    { label, click: onInstall },
    { type: "separator" },
  ]
}

export interface TrayMenuModel {
  activeWorkspaces: Workspace[]
  pendingStops: ReadonlySet<string>
  updateStatus: UpdateStatus
}

export interface TrayMenuActions {
  showDevsy: () => void
  showWorkspace: (id: string) => void
  showAllWorkspaces: () => void
  stopWorkspace: (id: string) => void
  installUpdate: () => void
  quit: () => void
}

export function buildTrayMenuTemplate(
  model: TrayMenuModel,
  actions: TrayMenuActions,
): Electron.MenuItemConstructorOptions[] {
  const active = model.activeWorkspaces
  const workspaceItems: Electron.MenuItemConstructorOptions[] = active
    .slice(0, 10)
    .map((workspace) => {
      const pending = model.pendingStops.has(workspace.id)
      const busy = workspace.status?.trim().toLowerCase() === "busy"
      return {
        label: `${workspace.id}${busy && !pending ? " — Busy" : ""}`,
        submenu: [
          {
            label: "Open in Devsy",
            click: () => actions.showWorkspace(workspace.id),
          },
          {
            label: pending ? "Stopping…" : "Stop Workspace",
            enabled: !pending,
            click: pending
              ? undefined
              : () => actions.stopWorkspace(workspace.id),
          },
        ],
      }
    })

  const activeSubmenu: Electron.MenuItemConstructorOptions[] =
    workspaceItems.length > 0
      ? [
          ...workspaceItems,
          ...(active.length > 10 ? [{ type: "separator" as const }] : []),
          {
            label: "Show All Workspaces…",
            click: actions.showAllWorkspaces,
          },
        ]
      : [
          { label: "No Active Workspaces", enabled: false },
          { type: "separator" },
          {
            label: "Open Workspaces in Devsy…",
            click: actions.showAllWorkspaces,
          },
        ]

  return [
    { label: "Show Devsy", click: actions.showDevsy },
    { type: "separator" },
    { label: `Active Workspaces (${active.length})`, submenu: activeSubmenu },
    ...buildUpdateMenuItems(model.updateStatus, actions.installUpdate),
    { type: "separator" },
    { label: "Quit Devsy", click: actions.quit },
  ]
}

interface TrayDeps {
  state: DaemonState
  showDevsy: (route?: string) => void
  stopWorkspace: (workspaceId: string) => Promise<void>
  refreshWorkspace: (workspaceId: string) => Promise<void>
  refreshWorkspaces: () => Promise<void>
}

export class AppTray {
  private tray: Tray | null = null
  private pendingStops = new Set<string>()
  private unsubscribeWorkspaceState: (() => void) | null = null
  private unsubscribeUpdateStatus: (() => void) | null = null
  private readonly onThemeUpdated = (): void => {
    this.tray?.setImage(this.createTrayIcon())
  }

  constructor(private deps: TrayDeps) {}

  setup(): void {
    if (this.tray) return
    this.tray = new Tray(this.createTrayIcon())
    this.tray.setToolTip("Devsy — No active workspaces")
    this.unsubscribeWorkspaceState = this.deps.state.onWorkspacesChange(() =>
      this.rebuildMenu(),
    )
    this.unsubscribeUpdateStatus = onUpdateStatusChanged(() =>
      this.rebuildMenu(),
    )
    if (process.platform !== "darwin") {
      nativeTheme.on("updated", this.onThemeUpdated)
    }
    this.rebuildMenu()
  }

  destroy(): void {
    this.unsubscribeWorkspaceState?.()
    this.unsubscribeWorkspaceState = null
    this.unsubscribeUpdateStatus?.()
    this.unsubscribeUpdateStatus = null
    if (process.platform !== "darwin") {
      nativeTheme.off("updated", this.onThemeUpdated)
    }
    this.pendingStops.clear()
    this.tray?.destroy()
    this.tray = null
  }

  private createTrayIcon(): Electron.NativeImage {
    const trayDir = join(__dirname, "../../resources/tray")
    if (process.platform === "darwin") {
      const icon = nativeImage.createFromPath(
        join(trayDir, "icon-trayTemplate.png"),
      )
      icon.setTemplateImage(true)
      return icon
    }
    const variant = nativeTheme.shouldUseDarkColors ? "dark" : "light"
    return nativeImage.createFromPath(
      join(trayDir, `icon-tray-${variant}.png`),
    )
  }

  private rebuildMenu(): void {
    if (!this.tray) return
    const activeWorkspaces = this.deps.state
      .workspaceList()
      .filter((workspace) => isActiveWorkspaceStatus(workspace.status))

    const template = buildTrayMenuTemplate(
      {
        activeWorkspaces,
        pendingStops: this.pendingStops,
        updateStatus: getLastStatus(),
      },
      {
        showDevsy: () => this.deps.showDevsy(),
        showWorkspace: (id) =>
          this.deps.showDevsy(`/workspaces/${encodeURIComponent(id)}`),
        showAllWorkspaces: () => this.deps.showDevsy("/workspaces"),
        stopWorkspace: (id) => void this.stopFromTray(id),
        installUpdate: () =>
          void installUpdate().catch((error) =>
            console.warn("[tray] failed to install update:", error),
          ),
        quit: () => app.quit(),
      },
    )
    this.tray.setContextMenu(Menu.buildFromTemplate(template))
    const count = activeWorkspaces.length
    this.tray.setToolTip(
      `Devsy — ${count} active workspace${count === 1 ? "" : "s"}`,
    )
  }

  private async stopFromTray(workspaceId: string): Promise<void> {
    if (this.pendingStops.has(workspaceId)) return
    this.pendingStops.add(workspaceId)
    this.rebuildMenu()
    try {
      await this.deps.stopWorkspace(workspaceId)
      await this.deps.refreshWorkspace(workspaceId)
      await this.deps.refreshWorkspaces()
    } catch (error) {
      console.warn(`[tray] failed to stop workspace ${workspaceId}:`, error)
    } finally {
      this.pendingStops.delete(workspaceId)
      this.rebuildMenu()
    }
  }
}
