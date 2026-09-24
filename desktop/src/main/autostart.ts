import { existsSync } from "node:fs"
import { mkdir, rm, writeFile } from "node:fs/promises"
import { join } from "node:path"
import { app } from "electron"
import type { AppSettings } from "./app-settings.js"
import { AUTO_LAUNCH_ARG } from "./launch-context.js"
import { requestPortalBackground } from "./portal-background.js"

export interface AutostartApplyResult {
  applied: boolean
  enabled: boolean
  status: "enabled" | "disabled" | "denied" | "unavailable" | "error"
  detail?: string
}

export interface AutostartEnvironment {
  platform: string
  isFlatpak: boolean
  flatpakId?: string
  execPath: string
  homeDir: string
  packaged: boolean
  appImagePath?: string
  xdgConfigHome?: string
  hostXdgConfigHome?: string
}

export function detectAutostartEnvironment(): AutostartEnvironment {
  return {
    platform: process.platform,
    isFlatpak: Boolean(process.env.FLATPAK_ID),
    flatpakId: process.env.FLATPAK_ID,
    execPath: process.execPath,
    homeDir: app.getPath("home"),
    packaged: app.isPackaged,
    appImagePath: process.env.APPIMAGE,
    xdgConfigHome: process.env.XDG_CONFIG_HOME,
    hostXdgConfigHome: process.env.HOST_XDG_CONFIG_HOME,
  }
}

function autostartDir(env: AutostartEnvironment): string {
  return join(env.xdgConfigHome || join(env.homeDir, ".config"), "autostart")
}

function xdgDesktopFilePath(env: AutostartEnvironment): string {
  return join(autostartDir(env), "devsy.desktop")
}

// Inside Flatpak, XDG_CONFIG_HOME points at the app sandbox, but the portal
// writes the entry to the host autostart directory.
function flatpakDesktopFilePath(env: AutostartEnvironment): string {
  const configHome = env.hostXdgConfigHome || join(env.homeDir, ".config")
  return join(configHome, "autostart", `${env.flatpakId}.desktop`)
}

function desktopExecArg(value: string): string {
  return `"${value.replaceAll("\\", "\\\\").replaceAll('"', '\\"')}"`
}

function xdgDesktopEntry(execPath: string): string {
  return [
    "[Desktop Entry]",
    "Type=Application",
    "Name=Devsy",
    `Exec=${desktopExecArg(execPath)} ${desktopExecArg(AUTO_LAUNCH_ARG)}`,
    "X-GNOME-Autostart-enabled=true",
    "",
  ].join("\n")
}

export function readAutostartEnabled(
  env: AutostartEnvironment,
): boolean | undefined {
  if (env.platform === "darwin" || env.platform === "win32") {
    return app.getLoginItemSettings({ args: [AUTO_LAUNCH_ARG] }).openAtLogin
  }
  try {
    if (env.isFlatpak) {
      const file = flatpakDesktopFilePath(env)
      return existsSync(file)
    }
    return existsSync(xdgDesktopFilePath(env))
  } catch {
    return undefined
  }
}

export async function applyAutostart(
  settings: AppSettings,
  env: AutostartEnvironment,
): Promise<AutostartApplyResult> {
  if (env.platform === "darwin") {
    try {
      app.setLoginItemSettings({
        openAtLogin: settings.runAtStartup,
        openAsHidden: settings.openToTrayOnStartup,
      })
      return {
        applied: true,
        enabled: settings.runAtStartup,
        status: settings.runAtStartup ? "enabled" : "disabled",
      }
    } catch (error) {
      return {
        applied: false,
        enabled: false,
        status: "error",
        detail: errorMessage(error),
      }
    }
  }
  if (env.platform === "win32") {
    try {
      app.setLoginItemSettings({
        openAtLogin: settings.runAtStartup,
        args: [AUTO_LAUNCH_ARG],
      })
      return {
        applied: true,
        enabled: settings.runAtStartup,
        status: settings.runAtStartup ? "enabled" : "disabled",
      }
    } catch (error) {
      return {
        applied: false,
        enabled: false,
        status: "error",
        detail: errorMessage(error),
      }
    }
  }
  if (env.platform !== "linux") {
    return {
      applied: false,
      enabled: false,
      status: "unavailable",
      detail: `Run at startup is not supported on ${env.platform}.`,
    }
  }
  if (env.isFlatpak) {
    return applyFlatpakAutostart(settings, env)
  }
  return applyXdgAutostart(settings, env)
}

async function applyXdgAutostart(
  settings: AppSettings,
  env: AutostartEnvironment,
): Promise<AutostartApplyResult> {
  const file = xdgDesktopFilePath(env)
  try {
    if (settings.runAtStartup) {
      await mkdir(autostartDir(env), { recursive: true })
      await writeFile(
        file,
        xdgDesktopEntry(env.appImagePath || env.execPath),
        "utf-8",
      )
    } else {
      await rm(file, { force: true })
    }
    return {
      applied: true,
      enabled: settings.runAtStartup,
      status: settings.runAtStartup ? "enabled" : "disabled",
    }
  } catch (error) {
    return {
      applied: false,
      enabled: readAutostartEnabled(env) === true,
      status: "error",
      detail: errorMessage(error),
    }
  }
}

// Flatpak autostart must go through the Background portal so the user sees
// the system consent prompt. The portal has no removal call, so disabling
// removes the portal-created desktop file directly; --filesystem=home in the
// manifest makes it visible inside the sandbox. Denial is a normal result
// and is reported truthfully instead of claiming the setting is on.
async function applyFlatpakAutostart(
  settings: AppSettings,
  env: AutostartEnvironment,
): Promise<AutostartApplyResult> {
  if (!settings.runAtStartup) {
    try {
      await rm(flatpakDesktopFilePath(env), { force: true })
    } catch (error) {
      return {
        applied: false,
        enabled: readAutostartEnabled(env) === true,
        status: "error",
        detail: errorMessage(error),
      }
    }
    return { applied: true, enabled: false, status: "disabled" }
  }
  try {
    const response = await requestPortalBackground({
      reason: "Allow Devsy to start automatically after you sign in.",
      autostart: true,
      commandline: ["devsy-wrapper", AUTO_LAUNCH_ARG],
    })
    if (response.autostart) {
      return { applied: true, enabled: true, status: "enabled" }
    }
    return {
      applied: false,
      enabled: false,
      status: "denied",
      detail:
        "The system did not grant background autostart permission. Run at startup stays off; you can allow it from your desktop settings and try again.",
    }
  } catch (error) {
    return {
      applied: false,
      enabled: false,
      status: "error",
      detail: errorMessage(error),
    }
  }
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}
