import { describe, expect, it } from "vitest"
import {
  isAppNavigationRequest,
  isAppRoute,
  newWorkspaceRoute,
  settingsRoute,
  workspaceLogsRoute,
  workspaceRoute,
  workspacesRoute,
} from "../../shared/app-route.js"

describe("application routes", () => {
  it("builds shared application destinations", () => {
    expect(settingsRoute()).toBe("/settings")
    expect(workspacesRoute()).toBe("/workspaces")
    expect(newWorkspaceRoute()).toBe("/workspace/new")
    expect(workspaceRoute("api")).toBe("/workspaces/api")
    expect(workspaceRoute("api", "logs")).toBe("/workspaces/api?tab=logs")
    expect(workspaceRoute("new")).toBe("/workspaces/new")
  })

  it.each(["a/b", "hello world", "x#y?z", "雪"])(
    "encodes workspace ID %s as one path segment",
    (id) =>
      expect(workspaceRoute(id)).toBe(`/workspaces/${encodeURIComponent(id)}`),
  )

  it("accepts only internal route requests with positive safe IDs", () => {
    expect(isAppNavigationRequest({ id: 1, route: "/settings" })).toBe(true)
    expect(isAppNavigationRequest({ id: 1.5, route: "/settings" })).toBe(false)
    expect(isAppNavigationRequest({ id: 0, route: "/settings" })).toBe(false)
    expect(isAppRoute("//example.com")).toBe(false)
    expect(isAppRoute("/settings#external")).toBe(false)
  })
})

describe("workspace logs route", () => {
  const run = "11111111-1111-4111-8111-111111111111"
  const inv = "a1111111-0000-4000-8000-000000000001"

  it("encodes context, run and invocation as query parameters", () => {
    const route = workspaceLogsRoute("api", {
      context: "my ctx&x",
      runId: run,
      invocationId: inv,
    })
    expect(isAppRoute(route)).toBe(true)
    const url = new URL(route, "http://x")
    expect(url.pathname).toBe("/workspaces/api")
    expect(url.searchParams.get("tab")).toBe("logs")
    expect(url.searchParams.get("context")).toBe("my ctx&x")
    expect(url.searchParams.get("run")).toBe(run)
    expect(url.searchParams.get("invocation")).toBe(inv)
  })

  it("keeps the plain logs route when no run is known", () => {
    expect(workspaceLogsRoute("api")).toBe("/workspaces/api?tab=logs")
    expect(workspaceLogsRoute("api", { invocationId: inv })).toBe(
      "/workspaces/api?tab=logs",
    )
  })
})
