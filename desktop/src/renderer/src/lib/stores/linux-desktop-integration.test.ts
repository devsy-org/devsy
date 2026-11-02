import { get } from "svelte/store"
import { beforeEach, describe, expect, it, vi } from "vitest"
import type { LinuxDesktopIntegrationStatus } from "$shared/linux-desktop-integration.js"

const ipc = vi.hoisted(() => ({
  get: vi.fn(),
  install: vi.fn(),
  remove: vi.fn(),
}))

vi.mock("$lib/ipc/commands.js", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  getLinuxDesktopIntegration: ipc.get,
  installLinuxDesktopIntegration: ipc.install,
  removeLinuxDesktopIntegration: ipc.remove,
}))

import {
  addToApplications,
  integrationBusy,
  integrationError,
  integrationStatus,
  refreshIntegrationStatus,
  removeFromApplications,
  shouldPromptForIntegration,
} from "./linux-desktop-integration.js"

const fresh: LinuxDesktopIntegrationStatus = {
  supported: true,
  appImagePath: "/a/Devsy.AppImage",
  installed: false,
  needsUpdate: false,
}

beforeEach(() => {
  vi.clearAllMocks()
  integrationStatus.set(null)
  integrationError.set(undefined)
})

describe("shouldPromptForIntegration", () => {
  it("prompts a directly started AppImage once", () => {
    expect(shouldPromptForIntegration(fresh, false)).toBe(true)
    expect(shouldPromptForIntegration(fresh, true)).toBe(false)
  })

  it.each([
    ["not an AppImage", { supported: false }],
    ["already added", { installed: true }],
    ["another launcher exists", { conflict: "Devsy already has a launcher." }],
    ["the path is unusable", { problem: "contains '='" }],
  ])("does not prompt when %s", (_name, change) => {
    expect(shouldPromptForIntegration({ ...fresh, ...change }, false)).toBe(
      false,
    )
  })
})

describe("integration actions", () => {
  it("publishes the status returned by a successful install", async () => {
    ipc.install.mockResolvedValue({ status: { ...fresh, installed: true } })
    await addToApplications()

    expect(get(integrationStatus)?.installed).toBe(true)
    expect(get(integrationError)).toBeUndefined()
    expect(get(integrationBusy)).toBe(false)
  })

  it("keeps the action available and shows the failure", async () => {
    ipc.install.mockResolvedValue({
      status: fresh,
      error: "Could not write /x",
    })
    await addToApplications()

    expect(get(integrationStatus)?.installed).toBe(false)
    expect(get(integrationError)).toBe("Could not write /x")
  })

  it("reports a rejected IPC call without leaving the page busy", async () => {
    ipc.remove.mockRejectedValue(new Error("bridge down"))
    await removeFromApplications()

    expect(get(integrationError)).toBe("bridge down")
    expect(get(integrationBusy)).toBe(false)
  })

  it("keeps the previous status when a refresh fails", async () => {
    integrationStatus.set(fresh)
    ipc.get.mockRejectedValue(new Error("offline"))
    vi.spyOn(console, "warn").mockImplementation(() => {})

    expect(await refreshIntegrationStatus()).toBeNull()
    expect(get(integrationStatus)).toEqual(fresh)
  })
})
