import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/svelte"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { integrationError } from "$lib/stores/linux-desktop-integration.js"

const ipc = vi.hoisted(() => ({
  dismissed: false,
  supported: true,
  error: undefined as string | undefined,
}))
vi.mock("$lib/ipc/commands.js", async (original) => ({
  ...(await original<object>()),
  getAppSettings: async () => ({
    settings: { appImageIntegrationPromptDismissed: ipc.dismissed },
  }),
  setAppSettings: async () => {
    ipc.dismissed = true
  },
  getLinuxDesktopIntegration: async () => ({
    supported: ipc.supported,
    installed: false,
    needsUpdate: false,
  }),
  installLinuxDesktopIntegration: async () => ({
    status: { supported: true, installed: !ipc.error, needsUpdate: false },
    error: ipc.error,
  }),
}))
vi.mock("$lib/stores/settings.js", () => ({
  syncDesktopSettingsFromMain: async () => {},
}))

import AppImageIntegrationPrompt from "./AppImageIntegrationPrompt.svelte"

beforeEach(() => {
  ipc.dismissed = false
  ipc.supported = true
  ipc.error = undefined
  integrationError.set(undefined)
})
afterEach(cleanup)

describe("AppImage integration prompt", () => {
  it("persists its first appearance and does not prompt again after declining", async () => {
    render(AppImageIntegrationPrompt)
    const decline = await screen.findByRole("button", { name: "Not now" })
    await fireEvent.click(decline)
    cleanup()
    render(AppImageIntegrationPrompt)
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    expect(ipc.dismissed).toBe(true)
  })

  it("reports integration failures and allows retry in the open dialog", async () => {
    ipc.error = "Could not write the applications directory"
    render(AppImageIntegrationPrompt)
    await fireEvent.click(
      await screen.findByRole("button", { name: "Add to applications" }),
    )
    expect((await screen.findByRole("alert")).textContent).toBe(ipc.error)
    ipc.error = undefined
    await fireEvent.click(
      screen.getByRole("button", { name: "Add to applications" }),
    )
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  })

  it("does not offer integration outside an AppImage", async () => {
    ipc.supported = false
    render(AppImageIntegrationPrompt)
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    expect(ipc.dismissed).toBe(false)
  })
})
