// @vitest-environment node

import type {
  BrowserWindow,
  IpcMainInvokeEvent,
  WebContents,
  WebFrameMain,
} from "electron"
import { describe, expect, it, vi } from "vitest"
import { isTrustedSecretIpcSender } from "../secret-ipc-policy.js"

function fixture(frameURL = "file:///app/dist/renderer/index.html") {
  const frame = { url: frameURL } as WebFrameMain
  const contents = {
    isDestroyed: vi.fn(() => false),
    mainFrame: frame,
  } as unknown as WebContents
  const win = {
    isDestroyed: vi.fn(() => false),
    webContents: contents,
  } as unknown as BrowserWindow
  const event = { sender: contents, senderFrame: frame } as IpcMainInvokeEvent
  return { event, frame, contents, win }
}

describe("secret IPC sender policy", () => {
  it("accepts the packaged top-level file document and SPA hashes", () => {
    const { event, win } = fixture(
      "file:///app/dist/renderer/index.html#/settings?tab=secrets",
    )
    expect(
      isTrustedSecretIpcSender(
        event,
        win,
        "file:///app/dist/renderer/index.html",
      ),
    ).toBe(true)
  })

  it("accepts the configured development document and SPA hashes", () => {
    const { event, win } = fixture("http://localhost:5173/#/settings")
    expect(isTrustedSecretIpcSender(event, win, "http://localhost:5173/")).toBe(
      true,
    )
  })

  it.each([
    "https://example.com/",
    "file:///app/other.html",
    "http://localhost:5174/",
    "http://user:pass@localhost:5173/",
    "not a url",
  ])("rejects untrusted or malformed document %s", (url) => {
    const { event, win } = fixture(url)
    expect(isTrustedSecretIpcSender(event, win, "http://localhost:5173/")).toBe(
      false,
    )
  })

  it("rejects a different window and a subframe", () => {
    const { event, win } = fixture()
    const otherWindow = fixture().win
    expect(
      isTrustedSecretIpcSender(
        event,
        otherWindow,
        "file:///app/dist/renderer/index.html",
      ),
    ).toBe(false)

    const subframe = {
      url: "file:///app/dist/renderer/index.html",
    } as WebFrameMain
    expect(
      isTrustedSecretIpcSender(
        { sender: event.sender, senderFrame: subframe } as IpcMainInvokeEvent,
        win,
        "file:///app/dist/renderer/index.html",
      ),
    ).toBe(false)
  })

  it("rejects destroyed windows, destroyed web contents, and missing event fields", () => {
    const { event, win, contents } = fixture()
    expect(
      isTrustedSecretIpcSender(
        event,
        null,
        "file:///app/dist/renderer/index.html",
      ),
    ).toBe(false)
    expect(
      isTrustedSecretIpcSender(
        {} as IpcMainInvokeEvent,
        win,
        "file:///app/dist/renderer/index.html",
      ),
    ).toBe(false)
    vi.mocked(win.isDestroyed).mockReturnValue(true)
    expect(
      isTrustedSecretIpcSender(
        event,
        win,
        "file:///app/dist/renderer/index.html",
      ),
    ).toBe(false)
    vi.mocked(win.isDestroyed).mockReturnValue(false)
    vi.mocked(contents.isDestroyed).mockReturnValue(true)
    expect(
      isTrustedSecretIpcSender(
        event,
        win,
        "file:///app/dist/renderer/index.html",
      ),
    ).toBe(false)
  })
})
