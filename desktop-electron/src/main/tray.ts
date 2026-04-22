import { Tray, Menu, app, nativeImage, nativeTheme } from "electron"
import type { BrowserWindow } from "electron"
import { join } from "node:path"
import type { DaemonState } from "./state.js"

interface TrayDeps {
  state: DaemonState
  getMainWindow: () => BrowserWindow | null
}

export class AppTray {
  private tray: Tray | null = null
  private rebuildTimer: ReturnType<typeof setInterval> | null = null

  constructor(private deps: TrayDeps) {}

  setup(): void {
    const icon = this.createTrayIcon()
    this.tray = new Tray(icon)
    this.tray.setToolTip("Devsy")
    this.rebuildMenu()

    if (process.platform !== "darwin") {
      nativeTheme.on("updated", () => {
        if (this.tray) {
          this.tray.setImage(this.createTrayIcon())
        }
      })
    }

    this.tray.on("click", () => {
      const win = this.deps.getMainWindow()
      if (win) {
        win.show()
        win.focus()
      }
    })

    // Rebuild menu every 5 seconds
    this.rebuildTimer = setInterval(() => this.rebuildMenu(), 5000)
  }

  destroy(): void {
    if (this.rebuildTimer) {
      clearInterval(this.rebuildTimer)
      this.rebuildTimer = null
    }
    if (this.tray) {
      this.tray.destroy()
      this.tray = null
    }
  }

  private createTrayIcon(): Electron.NativeImage {
    const trayDir = join(__dirname, "../../resources/tray")
    if (process.platform === "darwin") {
      // macOS: use Template images — Electron auto-adapts to menu bar theme
      const iconPath = join(trayDir, "icon-trayTemplate.png")
      try {
        const icon = nativeImage.createFromPath(iconPath)
        icon.setTemplateImage(true)
        return icon
      } catch {
        return nativeImage.createEmpty()
      }
    }

    // Windows/Linux: pick light or dark variant based on system theme
    const variant = nativeTheme.shouldUseDarkColors ? "dark" : "light"
    const iconPath = join(trayDir, `icon-tray-${variant}.png`)
    try {
      return nativeImage.createFromPath(iconPath)
    } catch {
      return nativeImage.createEmpty()
    }
  }

  private rebuildMenu(): void {
    if (!this.tray) return

    const workspaces = this.deps.state.workspaceList()
    const count = workspaces.length
    const statusLabel = count === 0 ? "No workspaces" : `${count} workspace${count === 1 ? "" : "s"}`

    const template: Electron.MenuItemConstructorOptions[] = [
      { label: statusLabel, enabled: false },
    ]

    if (workspaces.length > 0) {
      template.push({ type: "separator" })
      for (const ws of workspaces.slice(0, 10)) {
        template.push({
          label: `  ${ws.id}`,
          click: () => {
            const win = this.deps.getMainWindow()
            if (win) {
              win.show()
              win.focus()
              win.webContents.send("navigate", `/workspaces/${ws.id}`)
            }
          },
        })
      }
      if (count > 10) {
        template.push({ label: `  ... and ${count - 10} more`, enabled: false })
      }
    }

    template.push(
      { type: "separator" },
      {
        label: "Show Devsy",
        click: () => {
          const win = this.deps.getMainWindow()
          if (win) {
            win.show()
            win.focus()
          }
        },
      },
      {
        label: "Hide",
        click: () => {
          this.deps.getMainWindow()?.hide()
        },
      },
      { type: "separator" },
      {
        label: "Quit Devsy",
        click: () => app.quit(),
      },
    )

    const menu = Menu.buildFromTemplate(template)
    this.tray.setContextMenu(menu)
    this.tray.setToolTip(`Devsy — ${statusLabel}`)
  }
}
