export interface WorkspaceLabelSource {
  id: string
  displayName?: unknown
  pro?: unknown
}

function nonEmptyString(value: unknown): string {
  return typeof value === "string" ? value.trim() : ""
}

function platformDisplayName(pro: unknown): string {
  if (!pro || typeof pro !== "object") return ""
  return nonEmptyString((pro as { displayName?: unknown }).displayName)
}

export function workspaceLabel(workspace: WorkspaceLabelSource): string {
  return (
    nonEmptyString(workspace.displayName) ||
    platformDisplayName(workspace.pro) ||
    workspace.id
  )
}
