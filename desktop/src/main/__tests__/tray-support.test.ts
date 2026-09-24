import { describe, expect, it } from "vitest"
import { isTrayHostAvailable } from "../tray-support.js"

describe("isTrayHostAvailable", () => {
  it("treats non-Linux platforms as tray-capable", async () => {
    await expect(isTrayHostAvailable({ platform: "darwin" })).resolves.toBe(
      true,
    )
    await expect(isTrayHostAvailable({ platform: "win32" })).resolves.toBe(true)
  })

  it("detects a registered StatusNotifierWatcher", async () => {
    await expect(
      isTrayHostAvailable({
        platform: "linux",
        getNameOwner: async () => ":1.42",
      }),
    ).resolves.toBe(true)
  })

  it("treats GNOME without a watcher extension as tray-incapable", async () => {
    await expect(
      isTrayHostAvailable({
        platform: "linux",
        getNameOwner: async () => {
          throw new Error("org.freedesktop.DBus.Error.NameHasNoOwner")
        },
      }),
    ).resolves.toBe(false)
  })

  it("times out a hung bus instead of suppressing the window", async () => {
    await expect(
      isTrayHostAvailable({
        platform: "linux",
        timeoutMs: 10,
        getNameOwner: () => new Promise<string>(() => {}),
      }),
    ).resolves.toBe(false)
  })
})
