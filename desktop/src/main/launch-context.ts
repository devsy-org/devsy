import type { AppSettings } from "./app-settings.js"

// Passed by every automatic-launch mechanism (Windows login item args, XDG
// autostart Exec line, Flatpak Background portal commandline) so the app can
// distinguish an automatic login launch from an explicit user launch. macOS
// reports the same through login item settings instead of argv.
export const AUTO_LAUNCH_ARG = "--opened-at-login"

export interface LaunchEnvironment {
  argv: readonly string[]
  platform: string
  wasOpenedAtLogin?: boolean
  wasOpenedAsHidden?: boolean
}

export function isAutomaticLoginLaunch(env: LaunchEnvironment): boolean {
  if (env.argv.includes(AUTO_LAUNCH_ARG)) return true
  if (env.platform === "darwin") {
    return env.wasOpenedAtLogin === true || env.wasOpenedAsHidden === true
  }
  return false
}

// Only an automatic login launch may open to the tray. An explicit launch
// always creates the window, otherwise clicking Devsy would appear to do
// nothing.
export function shouldSuppressInitialWindow(
  settings: AppSettings,
  automaticLoginLaunch: boolean,
): boolean {
  return (
    automaticLoginLaunch &&
    settings.runAtStartup &&
    settings.openToTrayOnStartup
  )
}
