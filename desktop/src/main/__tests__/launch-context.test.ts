import { describe, expect, it } from "vitest"
import {
  AUTO_LAUNCH_ARG,
  isAutomaticLoginLaunch,
  shouldSuppressInitialWindow,
} from "../launch-context.js"

const base = { runAtStartup: true, openToTrayOnStartup: true, trayNotifications: "failures" as const }

describe("isAutomaticLoginLaunch", () => {
  it("detects the launch argument on Windows and Linux", () => {
    expect(
      isAutomaticLoginLaunch({
        argv: ["/app/devsy", AUTO_LAUNCH_ARG],
        platform: "win32",
      }),
    ).toBe(true)
    expect(
      isAutomaticLoginLaunch({
        argv: ["/app/devsy", AUTO_LAUNCH_ARG],
        platform: "linux",
      }),
    ).toBe(true)
  })

  it("detects a macOS login launch from login item settings", () => {
    expect(
      isAutomaticLoginLaunch({
        argv: ["/app/devsy"],
        platform: "darwin",
        wasOpenedAtLogin: true,
      }),
    ).toBe(true)
    expect(
      isAutomaticLoginLaunch({
        argv: ["/app/devsy"],
        platform: "darwin",
        wasOpenedAsHidden: true,
      }),
    ).toBe(true)
  })

  it("treats an explicit launch as manual", () => {
    expect(
      isAutomaticLoginLaunch({ argv: ["/app/devsy"], platform: "linux" }),
    ).toBe(false)
    expect(
      isAutomaticLoginLaunch({
        argv: ["/app/devsy"],
        platform: "darwin",
        wasOpenedAtLogin: false,
        wasOpenedAsHidden: false,
      }),
    ).toBe(false)
  })

  it("ignores the macOS flags on other platforms", () => {
    expect(
      isAutomaticLoginLaunch({
        argv: ["/app/devsy"],
        platform: "linux",
        wasOpenedAtLogin: true,
      }),
    ).toBe(false)
  })
})

describe("shouldSuppressInitialWindow", () => {
  it("suppresses only an automatic launch with both toggles on", () => {
    expect(shouldSuppressInitialWindow(base, true)).toBe(true)
  })

  it("never suppresses an explicit launch", () => {
    expect(shouldSuppressInitialWindow(base, false)).toBe(false)
  })

  it("never suppresses when open-to-tray is off", () => {
    expect(
      shouldSuppressInitialWindow({ ...base, openToTrayOnStartup: false }, true),
    ).toBe(false)
  })

  it("never suppresses when run-at-startup is off", () => {
    expect(
      shouldSuppressInitialWindow(
        { ...base, runAtStartup: false, openToTrayOnStartup: false },
        true,
      ),
    ).toBe(false)
  })
})
