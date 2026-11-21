import type { BrowserWindow, IpcMainEvent } from "electron"
import { describe, expect, it, vi } from "vitest"
import { createRendererErrorHandler } from "../renderer-error.js"

function fixture(registeredValues: string[] = []) {
  const frame = {
    url: "file:///app/renderer/index.html#/workspace?token=secret",
  }
  const contents = { mainFrame: frame, isDestroyed: () => false }
  const win = {
    webContents: contents,
    isDestroyed: () => false,
  } as unknown as BrowserWindow
  const event = {
    sender: contents,
    senderFrame: frame,
  } as unknown as IpcMainEvent
  const log = vi.fn()
  let time = 0
  const handle = createRendererErrorHandler({
    getWindow: () => win,
    expectedDocumentURL: "file:///app/renderer/index.html",
    log,
    registeredValues: () => registeredValues,
    now: () => time,
  })
  return {
    frame,
    contents,
    win,
    event,
    log,
    handle,
    advance: (ms: number) => {
      time += ms
    },
  }
}

const summary = {
  kind: "error",
  message: "Uncaught renderer error",
  name: "TypeError",
}

describe("renderer error handler", () => {
  it("accepts only the trusted top-level app document", () => {
    const f = fixture()
    f.handle(f.event, summary)
    expect(f.log).toHaveBeenCalledWith(summary)
    f.handle({ ...f.event, sender: {} } as IpcMainEvent, summary)
    f.handle(
      { ...f.event, senderFrame: { ...f.frame } } as unknown as IpcMainEvent,
      summary,
    )
    f.frame.url = "https://evil.example/"
    f.handle(f.event, summary)
    expect(f.log).toHaveBeenCalledTimes(1)
  })

  it("rejects malformed, oversized, extra, or arbitrary fields", () => {
    const f = fixture()
    for (const payload of [
      null,
      [],
      "error",
      {},
      { ...summary, message: "x".repeat(100_000) },
      { ...summary, name: "x".repeat(100_000) },
      { ...summary, name: "SecretError" },
      { ...summary, stack: "/home/user/secret" },
      { ...summary, kind: "other" },
    ])
      f.handle(f.event, payload)
    expect(f.log).not.toHaveBeenCalled()
  })

  it("sanitizes an otherwise valid message again before logging", () => {
    const f = fixture()
    f.handle(f.event, {
      ...summary,
      message:
        "Workspace failed token=supersecret https://example.test/?key=hidden /Users/private/file",
    })
    expect(f.log).toHaveBeenCalledOnce()
    const logged = f.log.mock.calls[0][0].message as string
    expect(logged).toContain("Workspace failed")
    for (const secret of [
      "supersecret",
      "example.test",
      "hidden",
      "/Users/private",
    ]) {
      expect(logged).not.toContain(secret)
    }
  })

  it("masks passphrases in a trusted renderer report before logging", () => {
    const f = fixture()
    f.handle(f.event, {
      ...summary,
      message: "Unlock failed passphrase=report-canary retry",
    })
    expect(f.log).toHaveBeenCalledWith({
      ...summary,
      message: "Unlock failed [redacted] retry",
    })
  })

  it("masks a registered value before structural sanitization splits it", () => {
    const registered = "prefix-canary/suffix-canary"
    const f = fixture([registered])
    f.handle(f.event, {
      ...summary,
      message: `Workspace failed ${registered} retry`,
    })
    expect(f.log).toHaveBeenCalledWith({
      ...summary,
      message: "Workspace failed [redacted] retry",
    })
  })

  it("accepts a raw message up to the renderer limit and rejects longer", () => {
    const f = fixture()
    f.handle(f.event, { ...summary, message: "x".repeat(4096) })
    expect(f.log).toHaveBeenCalledOnce()
    f.handle(f.event, { ...summary, message: "x".repeat(4097) })
    expect(f.log).toHaveBeenCalledOnce()
  })

  it("limits valid reports and resets after a minute", () => {
    const f = fixture()
    for (let i = 0; i < 20; i++) f.handle(f.event, summary)
    expect(f.log).toHaveBeenCalledTimes(10)
    f.advance(59_999)
    f.handle(f.event, summary)
    expect(f.log).toHaveBeenCalledTimes(10)
    f.advance(1)
    f.handle(f.event, summary)
    expect(f.log).toHaveBeenCalledTimes(11)
  })
})
