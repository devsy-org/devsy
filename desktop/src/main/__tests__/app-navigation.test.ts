import { describe, expect, it, vi } from "vitest"

vi.mock("../logging.js", () => ({ mainLog: { debug: vi.fn() } }))

const { AppNavigationController } = await import("../app-navigation.js")

function makeHarness() {
  const send = vi.fn()
  const window = {
    isDestroyed: vi.fn(() => false),
    isMinimized: vi.fn(() => false),
    restore: vi.fn(),
    show: vi.fn(),
    focus: vi.fn(),
    webContents: { send },
  }
  let current: typeof window | null = window
  const createWindow = vi.fn(() => {
    current = window
  })
  const controller = new AppNavigationController({
    getWindow: () => current,
    createWindow,
  })
  return {
    controller,
    window,
    send,
    createWindow,
    setWindow: (value: typeof window | null) => {
      current = value
    },
  }
}

describe("AppNavigationController", () => {
  it("reveals a minimized existing window and retains the request until acknowledgment", () => {
    const h = makeHarness()
    h.window.isMinimized.mockReturnValue(true)
    h.controller.rendererDidBecomeReady()
    h.controller.open("/settings")
    expect(h.window.restore).toHaveBeenCalledOnce()
    expect(h.window.show).toHaveBeenCalledOnce()
    expect(h.window.focus).toHaveBeenCalledOnce()
    expect(h.send).toHaveBeenCalledWith("app-navigation-request", {
      id: 1,
      route: "/settings",
    })
    h.controller.rendererDidStartLoading()
    h.controller.rendererDidBecomeReady()
    expect(h.send).toHaveBeenCalledTimes(2)
    h.controller.navigationApplied({ id: 1, route: "/settings" })
    h.controller.rendererDidStartLoading()
    h.controller.rendererDidBecomeReady()
    expect(h.send).toHaveBeenCalledTimes(2)
  })

  it("retains navigation while creating a window and dispatches on readiness", () => {
    const h = makeHarness()
    h.setWindow(null)
    h.controller.open("/settings")
    expect(h.createWindow).toHaveBeenCalledOnce()
    expect(h.send).not.toHaveBeenCalled()
    h.controller.rendererDidBecomeReady()
    expect(h.send).toHaveBeenCalledWith("app-navigation-request", {
      id: 1,
      route: "/settings",
    })
  })

  it("shows an existing hidden window before dispatching navigation", () => {
    const h = makeHarness()
    h.controller.rendererDidBecomeReady()
    h.controller.open("/settings")
    expect(h.window.show).toHaveBeenCalledOnce()
    expect(h.send).toHaveBeenCalledOnce()
  })

  it("keeps only the latest route and ignores malformed or stale acknowledgments", () => {
    const h = makeHarness()
    h.controller.rendererDidBecomeReady()
    h.controller.open("/settings")
    h.controller.open("/workspaces/a%2Fb")
    h.controller.navigationApplied({ id: "2", route: "/workspaces/a%2Fb" })
    h.controller.navigationApplied({ id: 1, route: "/settings" })
    h.controller.navigationApplied({ id: 2, route: "/elsewhere" })
    h.controller.rendererDidStartLoading()
    h.controller.rendererDidBecomeReady()
    expect(h.send).toHaveBeenLastCalledWith("app-navigation-request", {
      id: 2,
      route: "/workspaces/a%2Fb",
    })
  })

  it("reveals without changing route when opened without a destination", () => {
    const h = makeHarness()
    h.controller.rendererDidBecomeReady()
    h.controller.open()
    expect(h.window.show).toHaveBeenCalledOnce()
    expect(h.send).not.toHaveBeenCalled()
    expect(h.createWindow).not.toHaveBeenCalled()
  })

  it("does not redeliver an outstanding route when opened without a destination", () => {
    const h = makeHarness()
    h.controller.rendererDidBecomeReady()
    h.controller.open("/settings")
    h.controller.open()
    expect(h.window.show).toHaveBeenCalledTimes(2)
    expect(h.send).toHaveBeenCalledOnce()
  })

  it("does not send malformed routes", () => {
    const h = makeHarness()
    h.controller.rendererDidBecomeReady()
    h.controller.open("//not-internal")
    expect(h.send).not.toHaveBeenCalled()
  })
})
