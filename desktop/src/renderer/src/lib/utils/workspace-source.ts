export type WorkspaceSourceType = "git" | "local" | "image"
export type GitRefType = "branch" | "commit" | "pr"

export interface GitSourceForm {
  sourceType: "git"
  repoUrl: string
  refType: GitRefType
  refValue: string
  subPath: string
  devcontainerPath: string
  prebuildRepository: string
}

export interface LocalSourceForm {
  sourceType: "local"
  localPath: string
  devcontainerPath: string
  prebuildRepository: string
}

export interface ImageSourceForm {
  sourceType: "image"
  imageRef: string
}

export type WorkspaceSourceForm =
  | GitSourceForm
  | LocalSourceForm
  | ImageSourceForm

export interface WorkspaceSourceResult {
  source: string
  devcontainerPath?: string
  prebuildRepository?: string
}

// hostFromUrl extracts the hostname from SSH (git@host:path) and URL
// (scheme://[user@]host/path) forms.
function hostFromUrl(repoUrl: string): string {
  let s = repoUrl
  const scheme = s.indexOf("://")
  if (scheme !== -1) s = s.slice(scheme + 3)
  const at = s.indexOf("@")
  if (at !== -1) s = s.slice(at + 1)
  const sep = s.search(/[/:]/)
  if (sep !== -1) s = s.slice(0, sep)
  return s
}

// GitLab exposes merge requests at merge-requests/N/head; every other host
// uses pull/N/head.
function prRefspec(repoUrl: string, number: string): string {
  const segment = /gitlab/i.test(hostFromUrl(repoUrl)) ? "merge-requests" : "pull"
  return `@${segment}/${number}/head`
}

function refSuffix(
  refType: GitRefType,
  refValue: string,
  repoUrl: string,
): string {
  const value = refValue.trim()
  if (!value) return ""
  switch (refType) {
    case "branch":
      return `@${value}`
    case "commit":
      return `@sha256:${value}`
    case "pr":
      return prRefspec(repoUrl, value)
  }
}

function subPathSuffix(subPath: string): string {
  const value = subPath.trim()
  return value ? `@subpath:${value}` : ""
}

function optional(value: string): string | undefined {
  const trimmed = value.trim()
  return trimmed ? trimmed : undefined
}

export function buildWorkspaceSource(
  form: WorkspaceSourceForm,
): WorkspaceSourceResult {
  if (form.sourceType === "image") {
    return { source: form.imageRef.trim() }
  }

  if (form.sourceType === "local") {
    return {
      source: form.localPath.trim(),
      devcontainerPath: optional(form.devcontainerPath),
      prebuildRepository: optional(form.prebuildRepository),
    }
  }

  const repoUrl = form.repoUrl.trim()
  const source = `${repoUrl}${refSuffix(form.refType, form.refValue, repoUrl)}${subPathSuffix(form.subPath)}`

  return {
    source,
    devcontainerPath: optional(form.devcontainerPath),
    prebuildRepository: optional(form.prebuildRepository),
  }
}
