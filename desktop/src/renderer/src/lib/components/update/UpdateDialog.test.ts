import { render } from "@testing-library/svelte"
import { beforeEach, describe, expect, it, vi } from "vitest"

vi.mock("$lib/ipc/commands.js", () => ({
  checkForUpdates: vi.fn(),
  downloadUpdate: vi.fn(),
  installUpdate: vi.fn(),
}))
vi.mock("$lib/ipc/events.js", async (importOriginal) => {
  const mod = await importOriginal<typeof import("$lib/ipc/events.js")>()
  return { ...mod, onUpdateStatus: async () => () => {} }
})

import {
  __setForTest,
  initUpdateStore,
} from "$lib/stores/updates.svelte.js"
import UpdateDialog from "./UpdateDialog.svelte"

function bodyText(): string {
  return document.body.textContent ?? ""
}

function queryButton(label: RegExp): HTMLButtonElement | null {
  return (
    Array.from(document.querySelectorAll("button")).find((b) =>
      label.test(b.textContent ?? ""),
    ) ?? null
  )
}

describe("UpdateDialog", () => {
  beforeEach(async () => {
    await initUpdateStore()
  })

  it("renders 'checking' state", () => {
    __setForTest({ state: "checking", currentVersion: "1.0.0" })
    render(UpdateDialog, { props: { open: true } })
    expect(bodyText()).toMatch(/checking for updates/i)
  })

  it("renders 'downloaded' state with restart CTA", () => {
    __setForTest({
      state: "downloaded",
      currentVersion: "1.0.0",
      availableVersion: "9.9.9",
    })
    render(UpdateDialog, { props: { open: true } })
    expect(bodyText()).toMatch(/version 9\.9\.9/i)
    expect(queryButton(/restart/i)).toBeTruthy()
  })

  it("renders 'downloading' progress", () => {
    __setForTest({
      state: "downloading",
      currentVersion: "1.0.0",
      availableVersion: "9.9.9",
      progress: {
        percent: 42,
        bytesPerSecond: 1_500_000,
        transferred: 1,
        total: 2,
      },
    })
    render(UpdateDialog, { props: { open: true } })
    expect(bodyText()).toMatch(/42%/)
    expect(bodyText()).toMatch(/1\.50 MB\/s/)
  })

  it("renders 'error' with retry", () => {
    __setForTest({
      state: "error",
      currentVersion: "1.0.0",
      error: "404 from CDN",
      code: "feed-error",
    })
    render(UpdateDialog, { props: { open: true } })
    expect(bodyText()).toMatch(/404 from cdn/i)
    expect(queryButton(/try again|check again/i)).toBeTruthy()
  })

  it("renders dev-mode hint in not-available + dev-mode", () => {
    __setForTest({ state: "not-available", currentVersion: "1.0.0", code: "dev-mode" })
    render(UpdateDialog, { props: { open: true } })
    expect(bodyText()).toMatch(/packaged builds/i)
  })
})
