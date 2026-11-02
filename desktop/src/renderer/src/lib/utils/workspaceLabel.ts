import type { Workspace } from "$lib/types/index.js"
import { workspaceLabel } from "$shared/workspace-label.js"
import {
  normalizeDisplayLabel,
  SSH_HOST_SUFFIX,
} from "$shared/workspace-presentation.js"

export { workspaceLabel }

export function canonicalSshHost(workspace: Workspace): string {
  return workspace.canonicalSSHHost || `${workspace.id}${SSH_HOST_SUFFIX}`
}

export function preferredSshHost(workspace: Workspace): string {
  if (workspace.preferredSSHHost) return workspace.preferredSSHHost
  if (workspace.pro || !workspace.sshHostAlias) {
    return canonicalSshHost(workspace)
  }
  return `${workspace.sshHostAlias}${SSH_HOST_SUFFIX}`
}

export function workspaceMatchesQuery(
  workspace: Workspace,
  query: string,
): boolean {
  const needle = query.trim().toLowerCase()
  if (needle === "") return true
  const candidates = [
    workspaceLabel(workspace),
    workspace.id,
    preferredSshHost(workspace),
    workspace.sshHostAlias,
  ]
  return candidates.some(
    (candidate) => !!candidate && candidate.toLowerCase().includes(needle),
  )
}

export function findLabelConflict(
  label: string,
  workspaces: Workspace[],
  ownId: string,
): Workspace | undefined {
  const needle = normalizeDisplayLabel(label).toLowerCase()
  if (needle === "") return undefined
  return workspaces.find(
    (workspace) =>
      workspace.id !== ownId &&
      normalizeDisplayLabel(workspaceLabel(workspace)).toLowerCase() === needle,
  )
}

export function findAliasConflict(
  alias: string,
  workspaces: Workspace[],
  ownId: string,
): Workspace | undefined {
  const trimmed = alias.trim().toLowerCase()
  if (trimmed === "") return undefined
  const host = `${trimmed}${SSH_HOST_SUFFIX}`
  return workspaces.find(
    (workspace) =>
      workspace.id !== ownId &&
      [canonicalSshHost(workspace), preferredSshHost(workspace)].some(
        (candidate) => candidate.toLowerCase() === host,
      ),
  )
}
