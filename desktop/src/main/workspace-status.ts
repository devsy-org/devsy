export function normalizeWorkspaceStatus(raw: string): string | undefined {
  const text = raw.trim()
  if (!text) return undefined

  try {
    const parsed: unknown = JSON.parse(text)
    if (
      parsed !== null &&
      typeof parsed === "object" &&
      "state" in parsed &&
      typeof parsed.state === "string" &&
      parsed.state.trim()
    ) {
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
