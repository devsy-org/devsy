import type { ProviderSource } from "$lib/types/index.js"

export function dependencyToolForBuiltin(
  source: ProviderSource | string | undefined,
): string | undefined {
  if (
    typeof source !== "string" &&
    (source?.github || source?.file || source?.url)
  )
    return undefined
  const name = typeof source === "string" ? source : source?.raw
  switch (name) {
    case "docker":
    case "podman":
      return name
    case "apple":
      return "apple-container"
    case "kubernetes":
      return "kubernetes-config"
    default:
      return undefined
  }
}

export function providerSourceReference(
  source?: ProviderSource,
): string | undefined {
  const reference =
    source?.raw || source?.github || source?.url || source?.file || undefined
  if (!reference) return reference

  const displayCandidate = reference.trim()
  if (!/^https?:\/\//i.test(displayCandidate)) return reference

  try {
    const url = new URL(displayCandidate)
    const hiddenParameters =
      url.search || url.hash ? " (parameters hidden)" : ""
    return `${url.origin}${url.pathname}${hiddenParameters}`
  } catch {
    return "URL source"
  }
}
