import { join } from "node:path"
import { app, BrowserWindow, session } from "electron"
import { initAnalytics, shutdownAnalytics, trackEvent } from "./analytics.js"
import { CliRunner } from "./cli.js"
import { registerIpcHandlers } from "./ipc.js"
import { LogStore } from "./log-store.js"
import { PtyManager } from "./pty.js"
import { DaemonState } from "./state.js"
import { AppTray } from "./tray.js"
import { initAutoUpdater } from "./updater.js"
import { Watcher } from "./watcher.js"

const PROTOCOL = "devsy"

let mainWindow: BrowserWindow | null = null
let pendingDeepLink: string | null = null
const state = new DaemonState()

function handleDeepLink(url: string): void {
  if (mainWindow) {
    if (mainWindow.isMinimized()) mainWindow.restore()
    mainWindow.show()
    mainWindow.focus()
    mainWindow.webContents.send("deep-link", url)
  } else {
    pendingDeepLink = url
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
  } else if (mainWindow) {
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
      !(app as typeof app & { isQuitting?: boolean }).isQuitting
    ) {
      event.preventDefault()
      mainWindow.hide()
    }
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

  // Initialize log store and prune old logs
  const logStore = LogStore.defaultPath()
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

  app.on("before-quit", () => {
    ;(app as typeof app & { isQuitting?: boolean }).isQuitting = true
    trackEvent("app_close")
    shutdownAnalytics().catch(() => {})
    ptyManager.destroyAll()
  })

  // Register IPC handlers
  registerIpcHandlers({
    cli,
    state,
    logStore,
    pty: ptyManager,
    getMainWindow: () => mainWindow,
  })

  // Start state watcher
  const watcher = new Watcher({
    cli,
    state,
    getMainWindow: () => mainWindow,
  })
  watcher.start()

  // Set up system tray
  const appTray = new AppTray({
    state,
    getMainWindow: () => mainWindow,
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
