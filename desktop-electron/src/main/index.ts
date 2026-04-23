import { app, BrowserWindow } from "electron"
import { join } from "node:path"
import { CliRunner } from "./cli.js"
import { DaemonState } from "./state.js"
import { LogStore } from "./log-store.js"
import { registerIpcHandlers } from "./ipc.js"
import { PtyManager } from "./pty.js"
import { Watcher } from "./watcher.js"
import { AppTray } from "./tray.js"

let mainWindow: BrowserWindow | null = null
const state = new DaemonState()

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
    if (mainWindow && !(app as typeof app & { isQuitting?: boolean }).isQuitting) {
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
  })
}

app.whenReady().then(() => {
  // Resolve CLI binary: env override for testing, otherwise bundled in resources
  const binaryPath =
    process.env.DEVPOD_CLI_PATH ||
    (app.isPackaged
      ? CliRunner.resolveBinaryPath(process.resourcesPath)
      : CliRunner.resolveBinaryPath(join(__dirname, '../../resources')))
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
