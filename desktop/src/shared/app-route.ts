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
