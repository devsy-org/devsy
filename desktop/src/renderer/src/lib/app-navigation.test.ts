import { beforeEach, describe, expect, it, vi } from "vitest"

const appNavigationApplied = vi.fn().mockResolvedValue(undefined)
vi.mock("$lib/ipc/commands.js", async (importOriginal) => ({
  ...(await importOriginal<typeof import("$lib/ipc/commands.js")>()),
  appNavigationApplied,
}))

const { applyAppNavigationRequest } = await import("./app-navigation.js")

describe("renderer application navigation", () => {
  let requestId = 1

  beforeEach(() => {
    appNavigationApplied.mockClear()
    window.location.hash = "#/"
  })

  it.each([
    ["/settings", "#/settings"],
    ["/workspaces/api", "#/workspaces/api"],
    ["/workspaces/api?tab=logs", "#/workspaces/api?tab=logs"],
  ])(
    "applies %s to the observable hash before acknowledging",
    async (route, hash) => {
      const id = requestId++
      expect(await applyAppNavigationRequest({ id, route })).toBe(true)
      expect(window.location.hash).toBe(hash)
      expect(appNavigationApplied).toHaveBeenCalledWith({ id, route })
    },
  )

  it("acknowledges a repeated request after confirming the current hash", async () => {
    window.location.hash = "#/settings"
    expect(
      await applyAppNavigationRequest({ id: requestId++, route: "/settings" }),
    ).toBe(true)
    expect(appNavigationApplied).toHaveBeenCalledOnce()
  })

  it("does not acknowledge an older request after a newer one supersedes it", async () => {
    const older = { id: requestId++, route: "/settings" }
    const newer = { id: requestId++, route: "/workspaces/api?tab=logs" }
    const olderResult = applyAppNavigationRequest(older)
    const newerResult = applyAppNavigationRequest(newer)
    expect(await olderResult).toBe(false)
    expect(await newerResult).toBe(true)
    expect(window.location.hash).toBe("#/workspaces/api?tab=logs")
    expect(appNavigationApplied).toHaveBeenCalledOnce()
    expect(appNavigationApplied).toHaveBeenCalledWith(newer)
  })

  it.each([
    null,
    { id: 0, route: "/settings" },
    { id: 3, route: "https://example.com" },
  ])("ignores malformed request %j", async (request) => {
    expect(await applyAppNavigationRequest(request)).toBe(false)
    expect(appNavigationApplied).not.toHaveBeenCalled()
  })
})
