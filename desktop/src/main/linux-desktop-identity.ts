import { join } from "node:path"

export const PACKAGED_ICON_FILE = "app-icon.png"

interface WindowIconContext {
  platform: NodeJS.Platform
  isPackaged: boolean
  resourcesPath: string
  appPath: string
}

// Wayland and X11 shells resolve the window icon from the installed desktop
// entry; this only supplies the icon Electron sets on the window itself. macOS
// and Windows take theirs from the packaged bundle.
export function linuxWindowIcon(
  context: WindowIconContext,
): string | undefined {
  if (context.platform !== "linux") return undefined
  if (context.isPackaged) {
    return join(context.resourcesPath, PACKAGED_ICON_FILE)
  }
  return join(context.appPath, "resources", "icon.png")
}
