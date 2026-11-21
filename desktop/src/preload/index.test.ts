import { describe, expect, it, vi } from "vitest"
import "./index.js"

const electron = vi.hoisted(() => ({
  exposeInMainWorld: vi.fn(),
  send: vi.fn(),
  invoke: vi.fn(),
  on: vi.fn(),
  removeListener: vi.fn(),
}))

vi.mock("electron", () => ({
  contextBridge: { exposeInMainWorld: electron.exposeInMainWorld },
  ipcRenderer: {
    send: electron.send,
    invoke: electron.invoke,
    on: electron.on,
    removeListener: electron.removeListener,
  },
}))

describe("preload renderer error bridge", () => {
  it("sends the summary on the dedicated channel", () => {
    expect(electron.exposeInMainWorld).toHaveBeenCalledWith(
      "electronAPI",
      expect.any(Object),
    )
    const api = electron.exposeInMainWorld.mock.calls[0][1] as {
      reportRendererError: (summary: unknown) => void
    }
    const summary = {
      kind: "error",
      message: "Uncaught renderer error",
      name: "Error",
    }
    api.reportRendererError(summary)
    expect(electron.send).toHaveBeenCalledWith("renderer-error", summary)
  })
})
