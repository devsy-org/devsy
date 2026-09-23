import { join } from "node:path"
import { app, Menu, nativeImage, nativeTheme, Tray } from "electron"
import {
  type WorkspaceJob,
  workspaceJobBusy,
  workspaceJobInterruptible,
  workspaceJobLabel,
} from "../shared/workspace-operation.js"
import type { AppSettings } from "./app-settings.js"
import type { DaemonState, Workspace } from "./state.js"
import {
  getLastStatus,
  installUpdate,
  onUpdateStatusChanged,
  type UpdateStatus,
} from "./updater.js"
import type { WorkspaceJobs } from "./workspace-jobs.js"
import {
  isActiveWorkspaceStatus,
  normalizeWorkspaceStatus,
} from "./workspace-status.js"

// Native menus do not scroll well, so the tray shows only the most recently
// used workspaces and links into the app for the rest.
export const TRAY_WORKSPACE_LIMIT = 5

export function buildUpdateMenuItems(
  status: UpdateStatus,
  onInstall: () => void,
): Electron.MenuItemConstructorOptions[] {
  const installationFailed =
    status.state === "error" && status.code === "install-failed"
  if (status.state !== "downloaded" && !installationFailed) return []
  const version = status.availableVersion ?? ""
  const label = installationFailed
    ? `Retry Install Update v${version}`
    : version
      ? `Update to ${version}`
      : "Restart"
  return [{ label, click: onInstall }, { type: "separator" }]
}

export type TrayWorkspaceState =
  | "running"
  | "stopped"
  | "failed"
  | "busy"
  | "unknown"

export function trayWorkspaceState(
  workspace: Workspace,
  job?: WorkspaceJob,
): TrayWorkspaceState {
  if (workspaceJobBusy(job)) return "busy"
  if (job?.error || job?.state === "failed") return "failed"
  return workspaceStatusState(workspace)
}

function workspaceStatusState(workspace: Workspace): TrayWorkspaceState {
  const status = normalizeWorkspaceStatus(workspace.status ?? "")?.toLowerCase()
  if (status === "running" || status === "busy") return "running"
  if (status === "stopped") return "stopped"
  if (status === "failed" || status === "error") return "failed"
  return "unknown"
}

const STATE_GLYPHS: Record<TrayWorkspaceState, string> = {
  running: "●",
  stopped: "○",
  failed: "✖",
  busy: "◐",
  unknown: "◌",
}

const STATE_TEXT: Record<TrayWorkspaceState, string> = {
  running: "Running",
  stopped: "Stopped",
  failed: "Failed",
  busy: "Busy",
  unknown: "Unknown",
}

// The header counts workspaces that are actually running, so it derives from
// the normalized workspace status rather than the tray row state: a retained
// failed job marks the row failed without making the workspace stop running.
export function countRunningWorkspaces(workspaces: Workspace[]): number {
  return workspaces.filter((workspace) =>
    isActiveWorkspaceStatus(normalizeWorkspaceStatus(workspace.status ?? "")),
  ).length
}

export interface TrayMenuModel {
  workspaces: Workspace[]
  jobs?: Record<string, WorkspaceJob>
  pendingStops: ReadonlySet<string>
  pendingStarts: ReadonlySet<string>
  updateStatus: UpdateStatus
  settings: AppSettings
}

export interface TrayMenuActions {
  showDevsy: () => void
  showWorkspace: (id: string) => void
  showWorkspaceLogs: (id: string) => void
  showAllWorkspaces: () => void
  showSettings: () => void
  startWorkspace: (id: string) => void
  stopWorkspace: (id: string) => void
  toggleRunAtStartup: () => void
  toggleOpenToTray: () => void
  installUpdate: () => void
  quit: () => void
}

// Action meaning lives in text labels because native menu-item images are
// best-effort on Windows and several Linux panels; glyphs only mark state.
export function buildTrayMenuTemplate(
  model: TrayMenuModel,
  actions: TrayMenuActions,
): Electron.MenuItemConstructorOptions[] {
  const jobs = model.jobs ?? {}
  const running = countRunningWorkspaces(model.workspaces)
  const header: Electron.MenuItemConstructorOptions[] = [
    {
      label:
        running === 0
          ? "Devsy — No running workspaces"
          : `Devsy — ${running} running workspace${running === 1 ? "" : "s"}`,
      enabled: false,
    },
    { type: "separator" },
  ]

  const shown = model.workspaces.slice(0, TRAY_WORKSPACE_LIMIT)
  const workspaceItems: Electron.MenuItemConstructorOptions[] = shown.map(
    (workspace) => {
      const job = jobs[workspace.id]
      const state = trayWorkspaceState(workspace, job)
      // A failed start can leave the workspace stopped and a failed stop can
      // leave it running, so lifecycle actions follow the workspace status.
      // Stop can interrupt a start or create, so those busy rows keep it.
      const actionState =
        state === "failed"
          ? workspaceStatusState(workspace)
          : state === "busy" && workspaceJobInterruptible(job)
            ? "running"
            : state
      const jobLabel = workspaceJobLabel(job)
      const submenu: Electron.MenuItemConstructorOptions[] = [
        {
          label: "Open in Devsy",
          click: () => actions.showWorkspace(workspace.id),
        },
      ]
      if (actionState === "running") {
        const stopping =
          model.pendingStops.has(workspace.id) ||
          (workspaceJobBusy(job) && !workspaceJobInterruptible(job))
        submenu.push({
          label: stopping ? `${jobLabel ?? "Stopping"}…` : "Stop Workspace",
          enabled: !stopping,
          click: stopping
            ? undefined
            : () => actions.stopWorkspace(workspace.id),
        })
      } else if (actionState === "stopped") {
        submenu.push({
          label: model.pendingStarts.has(workspace.id)
            ? "Starting…"
            : "Start Workspace",
          enabled: !model.pendingStarts.has(workspace.id),
          click: model.pendingStarts.has(workspace.id)
            ? undefined
            : () => actions.startWorkspace(workspace.id),
        })
      }
      if (state === "failed" || state === "busy" || state === "unknown") {
        submenu.push({
          label: "View Logs",
          click: () => actions.showWorkspaceLogs(workspace.id),
        })
      }
      return {
        label: `${STATE_GLYPHS[state]} ${workspace.id} — ${jobLabel && (state === "busy" || state === "failed") ? jobLabel : STATE_TEXT[state]}`,
        submenu,
      }
    },
  )
  if (workspaceItems.length === 0) {
    workspaceItems.push({ label: "No Workspaces", enabled: false })
  } else if (model.workspaces.length > TRAY_WORKSPACE_LIMIT) {
    workspaceItems.push(
      { type: "separator" },
      {
        label: `View All ${model.workspaces.length} Workspaces in Devsy`,
        click: actions.showAllWorkspaces,
      },
    )
  }

  return [
    ...header,
    ...workspaceItems,
    { type: "separator" },
    { label: "Open Devsy", click: actions.showDevsy },
    {
      label: "Preferences",
      submenu: [
        {
          label: "Run at Startup",
          type: "checkbox",
          checked: model.settings.runAtStartup,
          click: actions.toggleRunAtStartup,
        },
        {
          label: "Open to Tray on Startup",
          type: "checkbox",
          checked: model.settings.openToTrayOnStartup,
          enabled: model.settings.runAtStartup,
          click: actions.toggleOpenToTray,
        },
        { type: "separator" },
        { label: "Open Settings…", click: actions.showSettings },
      ],
    },
    ...buildUpdateMenuItems(model.updateStatus, actions.installUpdate),
    { type: "separator" },
    { label: "Quit Devsy", click: actions.quit },
  ]
}

interface TrayDeps {
  workspaceJobs?: WorkspaceJobs
  state: DaemonState
  showDevsy: (route?: string) => void
  getSettings: () => AppSettings
  toggleRunAtStartup: () => void
  toggleOpenToTray: () => void
  startWorkspace: (workspaceId: string) => Promise<void>
  stopWorkspace: (workspaceId: string) => Promise<void>
  refreshWorkspace: (workspaceId: string) => Promise<void>
  refreshWorkspaces: () => Promise<void>
}

export class AppTray {
  private tray: Tray | null = null
  private pendingStops = new Set<string>()
  private pendingStarts = new Set<string>()
  private unsubscribeWorkspaceState: (() => void) | null = null
  private unsubscribeWorkspaceJobs: (() => void) | null = null
  private unsubscribeUpdateStatus: (() => void) | null = null
  private readonly onThemeUpdated = (): void => {
    this.tray?.setImage(this.createTrayIcon())
  }

  constructor(private deps: TrayDeps) {}

  setup(): void {
    if (this.tray) return
    this.tray = new Tray(this.createTrayIcon())
    this.tray.setToolTip("Devsy — No running workspaces")
    this.unsubscribeWorkspaceState = this.deps.state.onWorkspacesChange(() =>
      this.rebuildMenu(),
    )
    this.unsubscribeWorkspaceJobs =
      this.deps.workspaceJobs?.onChange(() => this.rebuildMenu()) ?? null
    this.unsubscribeUpdateStatus = onUpdateStatusChanged(() =>
      this.rebuildMenu(),
    )
    if (process.platform !== "darwin") {
      nativeTheme.on("updated", this.onThemeUpdated)
    }
    this.rebuildMenu()
  }

  destroy(): void {
    this.unsubscribeWorkspaceJobs?.()
    this.unsubscribeWorkspaceJobs = null
    this.unsubscribeWorkspaceState?.()
    this.unsubscribeWorkspaceState = null
    this.unsubscribeUpdateStatus?.()
    this.unsubscribeUpdateStatus = null
    if (process.platform !== "darwin") {
      nativeTheme.off("updated", this.onThemeUpdated)
    }
    this.pendingStops.clear()
    this.pendingStarts.clear()
    this.tray?.destroy()
    this.tray = null
  }

  rebuildMenu(): void {
    if (!this.tray) return
    const jobs = this.deps.workspaceJobs?.snapshot() ?? {}
    const template = buildTrayMenuTemplate(
      {
        workspaces: this.deps.state.workspaceList(),
        pendingStops: this.pendingStops,
        pendingStarts: this.pendingStarts,
        jobs,
        updateStatus: getLastStatus(),
        settings: this.deps.getSettings(),
      },
      {
        showDevsy: () => this.deps.showDevsy(),
        showWorkspace: (id) =>
          this.deps.showDevsy(`/workspaces/${encodeURIComponent(id)}`),
        showWorkspaceLogs: (id) =>
          this.deps.showDevsy(`/workspaces/${encodeURIComponent(id)}?tab=logs`),
        showAllWorkspaces: () => this.deps.showDevsy("/workspaces"),
        showSettings: () => this.deps.showDevsy("/settings"),
        startWorkspace: (id) => void this.startFromTray(id),
        stopWorkspace: (id) => void this.stopFromTray(id),
        toggleRunAtStartup: () => this.deps.toggleRunAtStartup(),
        toggleOpenToTray: () => this.deps.toggleOpenToTray(),
        installUpdate: () =>
          void installUpdate().catch((error) =>
            console.warn("[tray] failed to install update:", error),
          ),
        quit: () => app.quit(),
      },
    )
    this.tray.setContextMenu(Menu.buildFromTemplate(template))
    const running = countRunningWorkspaces(this.deps.state.workspaceList())
    this.tray.setToolTip(
      running === 0
        ? "Devsy — No running workspaces"
        : `Devsy — ${running} running workspace${running === 1 ? "" : "s"}`,
    )
  }

  private async startFromTray(workspaceId: string): Promise<void> {
    if (this.pendingStarts.has(workspaceId)) return
    this.pendingStarts.add(workspaceId)
    this.rebuildMenu()
    try {
      await this.deps.startWorkspace(workspaceId)
    } catch (error) {
      console.warn(`[tray] failed to start workspace ${workspaceId}:`, error)
    } finally {
      await this.refreshAfterAction(workspaceId)
      this.pendingStarts.delete(workspaceId)
      this.rebuildMenu()
    }
  }

  private async stopFromTray(workspaceId: string): Promise<void> {
    if (this.pendingStops.has(workspaceId)) return
    this.pendingStops.add(workspaceId)
    this.rebuildMenu()
    try {
      await this.deps.stopWorkspace(workspaceId)
    } catch (error) {
      console.warn(`[tray] failed to stop workspace ${workspaceId}:`, error)
    } finally {
      await this.refreshAfterAction(workspaceId)
      this.pendingStops.delete(workspaceId)
      this.rebuildMenu()
    }
  }

  private async refreshAfterAction(workspaceId: string): Promise<void> {
    try {
      await this.deps.refreshWorkspace(workspaceId)
    } catch (error) {
      console.warn(`[tray] failed to refresh workspace ${workspaceId}:`, error)
    }
    try {
      await this.deps.refreshWorkspaces()
    } catch (error) {
      console.warn("[tray] failed to refresh workspaces:", error)
    }
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
    return nativeImage.createFromPath(join(trayDir, `icon-tray-${variant}.png`))
  }
}
