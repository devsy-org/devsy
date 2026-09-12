export function normalizeWorkspaceStatus(raw: string): string | undefined {
  const text = raw.trim()
  if (!text) return undefined

  try {
    const parsed = JSON.parse(text) as { state?: unknown }
    if (typeof parsed.state === "string" && parsed.state.trim()) {
      return parsed.state.trim()
    }
    return undefined
  } catch {
    // The CLI may return a plain-text status.
  }

  return text
}

export function isActiveWorkspaceStatus(status: string | undefined): boolean {
  const normalized = status?.trim().toLowerCase()
  return normalized === "running" || normalized === "busy"
}
