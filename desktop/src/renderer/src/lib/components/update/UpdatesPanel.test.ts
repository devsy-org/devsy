import { render } from "@testing-library/svelte"
import { tick } from "svelte"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

const getAppVersion = vi.fn()
const getReleaseChannel = vi.fn()
const setReleaseChannel = vi.fn()
const checkForUpdates = vi.fn()
const downloadUpdate = vi.fn()
const installUpdate = vi.fn()

vi.mock("$lib/ipc/commands.js", () => ({
  getAppVersion: (...a: unknown[]) => getAppVersion(...a),
  getReleaseChannel: (...a: unknown[]) => getReleaseChannel(...a),
  setReleaseChannel: (...a: unknown[]) => setReleaseChannel(...a),
  checkForUpdates: (...a: unknown[]) => checkForUpdates(...a),
  downloadUpdate: (...a: unknown[]) => downloadUpdate(...a),
  installUpdate: (...a: unknown[]) => installUpdate(...a),
}))

const toastSuccess = vi.fn()
const toastError = vi.fn()
vi.mock("$lib/stores/toasts.js", () => ({
  toasts: {
    success: (...a: unknown[]) => toastSuccess(...a),
    error: (...a: unknown[]) => toastError(...a),
  },
}))

vi.mock("./update-toasts.js", () => ({ markUserInitiated: vi.fn() }))

vi.mock("$lib/ipc/events.js", async (importOriginal) => {
  const mod = await importOriginal<typeof import("$lib/ipc/events.js")>()
  return { ...mod, onUpdateStatus: async () => () => {} }
})

import { __setForTest, initUpdateStore } from "$lib/stores/updates.svelte.js"
import UpdatesPanel from "./UpdatesPanel.svelte"

function cardButton(label: RegExp): HTMLButtonElement | null {
  return (
    Array.from(document.querySelectorAll<HTMLButtonElement>('button[role="radio"]')).find(
      (b) => label.test(b.textContent ?? ""),
    ) ?? null
  )
}

async function renderPanel(channel: "stable" | "beta") {
  getAppVersion.mockResolvedValue("1.2.3")
  getReleaseChannel.mockResolvedValue(channel)
  setReleaseChannel.mockResolvedValue(undefined)
  await initUpdateStore()
  __setForTest({ state: "not-available", currentVersion: "1.2.3", version: "1.2.3" })
  render(UpdatesPanel)
  // Let onMount's async version/channel loads resolve.
  await tick()
  await Promise.resolve()
  await tick()
}

describe("UpdatesPanel channel switching", () => {
  beforeEach(() => {
    getAppVersion.mockReset()
    getReleaseChannel.mockReset()
    setReleaseChannel.mockReset()
    toastSuccess.mockReset()
    toastError.mockReset()
  })

  afterEach(() => {
    document.body.innerHTML = ""
  })

  it("switches immediately and confirms via toast on a normal upgrade", async () => {
    await renderPanel("stable")

    cardButton(/Preview/)?.click()
    await tick()
    await Promise.resolve()

    expect(setReleaseChannel).toHaveBeenCalledWith("beta")
    expect(toastSuccess).toHaveBeenCalledWith("Switched to the Preview channel")
  })

  it("opens a confirmation instead of switching on a downgrade", async () => {
    await renderPanel("beta")

    cardButton(/Stable/)?.click()
    await tick()

    // Downgrade must wait for confirmation — no IPC call yet.
    expect(setReleaseChannel).not.toHaveBeenCalled()
    expect(document.body.textContent).toMatch(/switch to the stable channel\?/i)
  })

  it("reverts the selection and toasts on IPC failure", async () => {
    await renderPanel("stable")
    setReleaseChannel.mockRejectedValueOnce(new Error("boom"))

    const preview = cardButton(/Preview/)
    preview?.click()
    await tick()
    await Promise.resolve()
    await tick()

    expect(toastError).toHaveBeenCalled()
    // Selection reverts to Stable.
    expect(cardButton(/Stable/)?.getAttribute("aria-checked")).toBe("true")
    expect(cardButton(/Preview/)?.getAttribute("aria-checked")).toBe("false")
  })
})

describe("UpdatesPanel status display", () => {
  beforeEach(() => {
    getAppVersion.mockReset()
    getReleaseChannel.mockReset()
    setReleaseChannel.mockReset()
  })

  afterEach(() => {
    document.body.innerHTML = ""
  })

  it("renders up-to-date state with installed version and channel", async () => {
    getAppVersion.mockResolvedValue("1.17.0")
    getReleaseChannel.mockResolvedValue("stable")
    await initUpdateStore()
    __setForTest({ state: "up-to-date", currentVersion: "1.17.0" })
    render(UpdatesPanel)
    await tick()
    await Promise.resolve()
    await tick()

    expect(document.body.textContent).toMatch(/devsy is up to date/i)
    expect(document.body.textContent).toMatch(/version 1\.17\.0/i)
    expect(document.body.textContent).toMatch(/stable channel/i)
  })

  it("renders available update with both installed and available versions", async () => {
    getAppVersion.mockResolvedValue("1.17.0")
    getReleaseChannel.mockResolvedValue("stable")
    await initUpdateStore()
    __setForTest({
      state: "available",
      currentVersion: "1.17.0",
      availableVersion: "1.18.0",
    })
    render(UpdatesPanel)
    await tick()
    await Promise.resolve()
    await tick()

    expect(document.body.textContent).toMatch(/devsy 1\.18\.0 is available/i)
    expect(document.body.textContent).toMatch(/installed:\s*v1\.17\.0/i)
    expect(document.body.textContent).toMatch(/available:\s*v1\.18\.0/i)
    const btn = Array.from(document.querySelectorAll("button")).find((b) =>
      /download update/i.test(b.textContent ?? ""),
    )
    expect(btn).toBeTruthy()
  })

  it("renders downloaded state with 'Restart' CTA", async () => {
    getAppVersion.mockResolvedValue("1.17.0")
    getReleaseChannel.mockResolvedValue("stable")
    await initUpdateStore()
    __setForTest({
      state: "downloaded",
      currentVersion: "1.17.0",
      availableVersion: "1.18.0",
    })
    render(UpdatesPanel)
    await tick()
    await Promise.resolve()
    await tick()

    expect(document.body.textContent).toMatch(/devsy 1\.18\.0 is ready/i)
    const btn = Array.from(document.querySelectorAll("button")).find((b) =>
      /restart/i.test(b.textContent ?? ""),
    )
    expect(btn).toBeTruthy()
  })

  it("renders idle state when no check has run yet", async () => {
    getAppVersion.mockResolvedValue("1.17.0")
    getReleaseChannel.mockResolvedValue("stable")
    await initUpdateStore()
    __setForTest({ state: "idle", currentVersion: "1.17.0" })
    render(UpdatesPanel)
    await tick()
    await Promise.resolve()
    await tick()

    expect(document.body.textContent).toMatch(/no update check has run yet/i)
  })

  it("renders dev-mode notice for unpackaged builds", async () => {
    getAppVersion.mockResolvedValue("1.17.0")
    getReleaseChannel.mockResolvedValue("stable")
    await initUpdateStore()
    __setForTest({ state: "up-to-date", currentVersion: "1.17.0", code: "dev-mode" })
    render(UpdatesPanel)
    await tick()
    await Promise.resolve()
    await tick()

    expect(document.body.textContent).toMatch(/updates run in packaged builds/i)
  })

  it("renders channel-missing notice when channel has no releases", async () => {
    getAppVersion.mockResolvedValue("1.17.0")
    getReleaseChannel.mockResolvedValue("beta")
    await initUpdateStore()
    __setForTest({ state: "up-to-date", currentVersion: "1.17.0", code: "channel-missing" })
    render(UpdatesPanel)
    await tick()
    await Promise.resolve()
    await tick()

    expect(document.body.textContent).toMatch(/no releases on this channel yet/i)
  })
})
