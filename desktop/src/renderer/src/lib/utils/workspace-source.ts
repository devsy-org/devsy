export type WorkspaceSourceType = "git" | "local" | "image"
export type GitRefType = "branch" | "commit" | "pr"

export interface WorkspaceSourceForm {
  sourceType: WorkspaceSourceType
  repoUrl: string
  localPath: string
  imageRef: string
  refType: GitRefType
  refValue: string
  subPath: string
  devcontainerPath: string
  prebuildRepository: string
}

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

  const base =
    form.sourceType === "git" ? form.repoUrl.trim() : form.localPath.trim()

  const ref =
    form.sourceType === "git" ? refSuffix(form.refType, form.refValue) : ""

  const source = `${base}${ref}${subPathSuffix(form.subPath)}`

  return {
    source,
    devcontainerPath: optional(form.devcontainerPath),
    prebuildRepository: optional(form.prebuildRepository),
  }
}
