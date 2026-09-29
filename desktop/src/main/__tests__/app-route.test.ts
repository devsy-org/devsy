import { describe, expect, it } from "vitest"
import {
  isAppNavigationRequest,
  isAppRoute,
  newWorkspaceRoute,
  settingsRoute,
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
