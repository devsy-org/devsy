export type AppRoute = string

export type WorkspaceTab = "overview" | "logs" | "terminal"

export interface AppNavigationRequest {
  id: number
  route: AppRoute
}

export interface AppNavigationAcknowledgment extends AppNavigationRequest {}

export function isAppRoute(value: unknown): value is AppRoute {
  return (
    typeof value === "string" &&
    value.startsWith("/") &&
    !value.startsWith("//") &&
    !/[\s#\\]/u.test(value)
  )
}

export function isAppNavigationRequest(
  value: unknown,
): value is AppNavigationRequest {
  if (!value || typeof value !== "object") return false
  const request = value as Record<string, unknown>
  return (
    Number.isSafeInteger(request.id) &&
    (request.id as number) > 0 &&
    isAppRoute(request.route)
  )
}

export const settingsRoute = (): AppRoute => "/settings"

export const workspacesRoute = (): AppRoute => "/workspaces"

export const newWorkspaceRoute = (): AppRoute => "/workspace/new"

export function workspaceRoute(
  workspaceId: string,
  tab?: WorkspaceTab,
): AppRoute {
  const route = `/workspaces/${encodeURIComponent(workspaceId)}`
  return tab ? `${route}?tab=${encodeURIComponent(tab)}` : route
}

export interface WorkspaceLogsTarget {
  context?: string
  runId?: string
  invocationId?: string
}

/** Opens the Logs tab, optionally pinned to a retained run and invocation. */
export function workspaceLogsRoute(
  workspaceId: string,
  target: WorkspaceLogsTarget = {},
): AppRoute {
  const params = new URLSearchParams({ tab: "logs" })
  if (target.runId) {
    if (target.context) params.set("context", target.context)
    params.set("run", target.runId)
    if (target.invocationId) params.set("invocation", target.invocationId)
  }
  return `/workspaces/${encodeURIComponent(workspaceId)}?${params.toString()}`
}
