export const workspaceIdRule =
  "Use 1-48 lowercase letters (a-z), numbers, or dashes."

export function isValidWorkspaceId(id: string): boolean {
  return /^[a-z0-9-]{1,48}$/.test(id)
}

export function deriveWorkspaceId(source: string): string {
  const trimmed = source.trim()
  const isRepository =
    /^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed) ||
    /^[^/@\\]+@[^:/\\]+:/.test(trimmed) ||
    /^[^/@\\]+\.[^/@\\]+\//.test(trimmed)
  const repository = isRepository
    ? trimmed
        .replace(/^[^/@]+@[^:]+:/, "")
        .replace(/^[a-z][a-z0-9+.-]*:\/\/[^/]*\//i, "")
        .split("@")[0]
    : trimmed
  const leaf = repository.split(/[\\/]/).filter(Boolean).pop() ?? ""
  return (
    leaf
      .replace(/\.git$/i, "")
      .toLowerCase()
      .replace(/[^a-z0-9-]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 48)
      .replace(/-+$/g, "") || "workspace"
  )
}

export function uniqueWorkspaceId(base: string, ids: string[]): string {
  const existing = new Set(ids.map((id) => id.toLowerCase()))
  if (!existing.has(base.toLowerCase())) return base
  for (let i = 1; ; i++) {
    const suffix = `-${i}`
    const candidate = `${base.slice(0, 48 - suffix.length)}${suffix}`
    if (!existing.has(candidate.toLowerCase())) return candidate
  }
}
