import { mkdtempSync, readFileSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { afterEach, describe, expect, it, vi } from "vitest"

const { getLoginItemSettings } = vi.hoisted(() => ({
  getLoginItemSettings: vi.fn(() => ({ openAtLogin: false })),
}))
vi.mock("electron", () => ({
  app: {
    getPath: () => "/tmp",
    isPackaged: true,
    getLoginItemSettings,
  },
}))

import { applyAutostart, readAutostartEnabled } from "../autostart.js"

describe("Linux autostart", () => {
  let dir: string | undefined

  afterEach(() => {
    if (dir) rmSync(dir, { recursive: true, force: true })
    dir = undefined
    getLoginItemSettings.mockClear()
  })

  it("uses APPIMAGE and XDG_CONFIG_HOME for the desktop entry", async () => {
    dir = mkdtempSync(join(tmpdir(), "devsy-autostart-"))
    await applyAutostart(
      { runAtStartup: true, openToTrayOnStartup: true, trayNotifications: "all" },
      {
        platform: "linux",
        isFlatpak: false,
        execPath: "/tmp/mount/devsy",
        appImagePath: "/opt/Devsy.AppImage",
        homeDir: "/home/test",
        xdgConfigHome: dir,
        packaged: true,
      },
    )
    expect(readFileSync(join(dir, "autostart", "devsy.desktop"), "utf8")).toContain(
      'Exec="/opt/Devsy.AppImage" "--opened-at-login"',
    )
  })

  it("passes the autostart argument when reading Windows login status", () => {
    expect(
      readAutostartEnabled({
        platform: "win32",
        isFlatpak: false,
        execPath: "devsy.exe",
        homeDir: "/home/test",
        packaged: true,
      }),
    ).toBe(false)
    expect(getLoginItemSettings).toHaveBeenCalledWith({
      args: ["--opened-at-login"],
    })
  })
})
