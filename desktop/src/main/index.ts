import { homedir } from "node:os"
import { join } from "node:path"
import { app, BrowserWindow, session } from "electron"
import { initAnalytics, shutdownAnalytics, trackEvent } from "./analytics.js"
import { isAppQuitting, markAppQuitting } from "./app-lifecycle.js"
import { CliRunner } from "./cli.js"
import { DaemonManager } from "./daemon-manager.js"
import { registerIpcHandlers } from "./ipc.js"
import { LogStore } from "./log-store.js"
import { MachineDiagnosticsStore } from "./machine-diagnostics-store.js"
import { MachineDiagnosticsManager } from "./machine-diagnostics-manager.js"
import { ProviderJobs } from "./provider-jobs.js"
import { PtyManager } from "./pty.js"
import { DaemonState } from "./state.js"
import { AppTray } from "./tray.js"
import { initAutoUpdater, stopAutoUpdater } from "./updater.js"
import { Watcher } from "./watcher.js"
import { WorkspaceJobs } from "./workspace-jobs.js"

const PROTOCOL = "devsy"

let mainWindow: BrowserWindow | null = null
let pendingDeepLink: string | null = null
let pendingRoute: string | null = null
let rendererReady = false
let appTray: AppTray | null = null
let watcher: Watcher | null = null
const state = new DaemonState()

function handleDeepLink(url: string): void {
  if (mainWindow && !mainWindow.isDestroyed()) {
    if (mainWindow.isMinimized()) mainWindow.restore()
    mainWindow.show()
    mainWindow.focus()
    mainWindow.webContents.send("deep-link", url)
  } else {
    pendingDeepLink = url
  }
}

function showDevsy(route?: string): void {
  if (!mainWindow || mainWindow.isDestroyed()) {
    if (route) pendingRoute = route
    createWindow()
    return
  }
  if (mainWindow.isMinimized()) mainWindow.restore()
  mainWindow.show()
  mainWindow.focus()
  if (route) {
    if (rendererReady) mainWindow.webContents.send("navigate", route)
    else pendingRoute = route
  }
}

// Enforce single instance; forward deep links from second instances to the first.
const gotLock = app.requestSingleInstanceLock()
if (!gotLock) {
  app.quit()
}

app.on("second-instance", (_event, argv) => {
  const url = argv.find((arg) => arg.startsWith(`${PROTOCOL}://`))
  if (url) {
    handleDeepLink(url)
  } else if (mainWindow && !mainWindow.isDestroyed()) {
    if (mainWindow.isMinimized()) mainWindow.restore()
    mainWindow.show()
    mainWindow.focus()
  }
})

// macOS delivers protocol URLs via open-url (before or after ready).
app.on("open-url", (event, url) => {
  event.preventDefault()
  handleDeepLink(url)
})

function createWindow(): void {
  rendererReady = false
  mainWindow = new BrowserWindow({
    width: 1200,
    height: 800,
    minWidth: 1000,
    minHeight: 700,
    show: false,
    title: "Devsy",
    webPreferences: {
      preload: join(__dirname, "../preload/index.js"),
      contextIsolation: true,
      nodeIntegration: false,
    },
  })

  mainWindow.on("close", (event) => {
    if (
      mainWindow &&
      !isAppQuitting()
    ) {
      event.preventDefault()
      mainWindow.hide()
    }
  })
  mainWindow.webContents.on("did-start-loading", () => {
    rendererReady = false
  })

  if (process.env.ELECTRON_RENDERER_URL) {
    mainWindow.loadURL(process.env.ELECTRON_RENDERER_URL)
  } else {
    mainWindow.loadFile(join(__dirname, "../renderer/index.html"))
  }

  mainWindow.once("ready-to-show", () => {
    mainWindow?.show()
    if (pendingDeepLink) {
      mainWindow?.webContents.send("deep-link", pendingDeepLink)
      pendingDeepLink = null
    }
  })
}

app.whenReady().then(() => {
  initAnalytics()
  trackEvent("app_open")

  // Register devsy:// as the default protocol handler for this app.
  app.setAsDefaultProtocolClient(PROTOCOL)

  // Capture deep link from argv on Windows/Linux when launched via protocol URL.
  const startupUrl = process.argv.find((arg) =>
    arg.startsWith(`${PROTOCOL}://`),
  )
  if (startupUrl) pendingDeepLink = startupUrl

  // Apply Content Security Policy to all web responses.
  session.defaultSession.webRequest.onHeadersReceived((details, callback) => {
    callback({
      responseHeaders: {
        ...details.responseHeaders,
        "Content-Security-Policy": [
          "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self' ws://localhost:*",
        ],
      },
    })
  })

  // Resolve CLI binary: env override for testing, otherwise bundled in resources
  const binaryPath =
    process.env.DEVSY_CLI_PATH ||
    (app.isPackaged
      ? CliRunner.resolveBinaryPath(process.resourcesPath)
      : CliRunner.resolveBinaryPath(join(__dirname, "../../resources")))
  const cli = new CliRunner(binaryPath)

  // Initialize log store. Logs live under ~/.devsy/desktop/logs/ — inside the
  // shared ~/.devsy root (no artifact sprawl) but outside the CLI-managed
  // ~/.devsy/contexts/<ctx>/workspaces/<id>/ subtree that `workspace delete`
  // unlinks. That separation closes the file-deletion race by construction.
  const logStore = new LogStore(join(homedir(), ".devsy", "desktop", "logs"))
	const machineDiagnosticsStore = new MachineDiagnosticsStore(join(homedir(), ".devsy", "desktop", "diagnostics"))
	const machineDiagnosticsManager = new MachineDiagnosticsManager(cli, machineDiagnosticsStore)
  try {
    const pruned = logStore.prune(30)
    if (pruned > 0) console.log(`Pruned ${pruned} old log files`)
  } catch (e) {
    console.error("Failed to prune old logs:", e)
  }

  // Initialize PTY manager
  const ptyManager = new PtyManager({
    binaryPath: binaryPath,
    getMainWindow: () => mainWindow,
  })

  // Start local daemon for efficient polling
  const daemonManager = new DaemonManager(binaryPath)
  const daemonDisabled = process.env.DEVSY_DISABLE_DAEMON === "true"
  if (!daemonDisabled) {
    daemonManager.start()
  }

  app.on("before-quit", () => {
    markAppQuitting()
    trackEvent("app_close")
    shutdownAnalytics().catch(() => {})
    cli.killAll()
    for (const proc of tunnelProcesses.values()) {
      proc.kill("SIGTERM")
    }
    daemonManager.stop()
    ptyManager.destroyAll()
    stopAutoUpdater()
    watcher?.stop()
    appTray?.destroy()
  })

  const providerJobs = new ProviderJobs()
  const workspaceJobs = new WorkspaceJobs()

  // Register IPC handlers
  const {
    tunnelProcesses,
    scheduleProviderUpdateCheck,
    runInitialProviderUpdateCheck,
    workspaceActions,
  } = registerIpcHandlers({
    cli,
    state,
    logStore,
		machineDiagnosticsStore,
		machineDiagnosticsManager,
    pty: ptyManager,
    getMainWindow: () => mainWindow,
    providerJobs,
    workspaceJobs,
    onRendererReady: (sender) => {
      if (
        !mainWindow ||
        mainWindow.isDestroyed() ||
        mainWindow.webContents !== sender
      ) {
        return
      }
      rendererReady = true
      if (pendingRoute) {
        sender.send("navigate", pendingRoute)
        pendingRoute = null
      }
    },
    workspaceSnapshot: () => watcher?.workspaceSnapshot(),
  })

  // Start state watcher
  watcher = new Watcher({
    cli,
    daemon: daemonDisabled ? undefined : daemonManager.daemonClient,
    state,
    getMainWindow: () => mainWindow,
    providerJobs,
    workspaceJobs,
  })
  providerJobs.onChange(() => watcher?.broadcastProviders())
  providerJobs.setRefresh(() =>
    watcher ? watcher.refreshProviders() : Promise.resolve(),
  )
  workspaceJobs.onChange(() => watcher?.broadcastWorkspaces())
  workspaceJobs.setRefresh(async (id, job) => {
    if (!watcher) throw new Error("Workspace watcher unavailable")
    await watcher.refreshWorkspaces()
    const exists = state.workspaceList().some((workspace) => workspace.id === id)
    if (job.activity === "deleting" && !job.error) {
      if (exists) throw new Error("Workspace list has not caught up yet")
    } else if (exists) {
      await watcher.refreshWorkspaceStatus(id)
    } else if (!job.error) {
      throw new Error("Workspace not yet present in the list")
    }
    watcher.broadcastWorkspaces()
  })

  void watcher.start().then(runInitialProviderUpdateCheck)
  scheduleProviderUpdateCheck()

  // Set up system tray
  appTray = new AppTray({
    state,
    workspaceJobs,
    showDevsy,
    stopWorkspace: workspaceActions.stop,
    refreshWorkspace: (id) =>
      watcher ? watcher.refreshWorkspaceStatus(id) : Promise.resolve(),
    refreshWorkspaces: () =>
      watcher ? watcher.refreshWorkspaces() : Promise.resolve(),
  })
  appTray.setup()

  createWindow()

  if (app.isPackaged) {
    initAutoUpdater(() => mainWindow)
  }

  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      createWindow()
    }
  })
})

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit()
  }
})

export { mainWindow, state }
