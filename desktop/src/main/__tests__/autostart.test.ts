import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs"
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
      {
        runAtStartup: true,
        openToTrayOnStartup: true,
        trayNotifications: "all",
      },
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
    expect(
      readFileSync(join(dir, "autostart", "devsy.desktop"), "utf8"),
    ).toContain('Exec="/opt/Devsy.AppImage" "--opened-at-login"')
  })

  it("removes the Flatpak portal entry from the host autostart directory", async () => {
    dir = mkdtempSync(join(tmpdir(), "devsy-autostart-"))
    const hostConfig = join(dir, "host-config")
    const sandboxConfig = join(dir, "sandbox-config")
    const entry = join(hostConfig, "autostart", "sh.devsy.app.desktop")
    mkdirSync(join(hostConfig, "autostart"), { recursive: true })
    writeFileSync(entry, "[Desktop Entry]\n")
    const env = {
      platform: "linux",
      isFlatpak: true,
      flatpakId: "sh.devsy.app",
      execPath: "/app/bin/devsy",
      homeDir: dir,
      xdgConfigHome: sandboxConfig,
      hostXdgConfigHome: hostConfig,
      packaged: true,
    }
    expect(readAutostartEnabled(env)).toBe(true)

    const result = await applyAutostart(
      {
        runAtStartup: false,
        openToTrayOnStartup: true,
        trayNotifications: "all",
      },
      env,
    )

    expect(result.status).toBe("disabled")
    expect(existsSync(entry)).toBe(false)
    expect(readAutostartEnabled(env)).toBe(false)
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
