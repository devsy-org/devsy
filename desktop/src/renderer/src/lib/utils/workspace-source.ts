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

function refSuffix(refType: GitRefType, refValue: string): string {
  const value = refValue.trim()
  if (!value) return ""
  switch (refType) {
    case "branch":
      return `@${value}`
    case "commit":
      return `@sha256:${value}`
    case "pr":
      return `@pull/${value}/head`
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

  const source = `${form.repoUrl.trim()}${refSuffix(form.refType, form.refValue)}${subPathSuffix(form.subPath)}`

  return {
    source,
    devcontainerPath: optional(form.devcontainerPath),
    prebuildRepository: optional(form.prebuildRepository),
  }
}
