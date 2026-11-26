import type { ProviderSource } from "$lib/types/index.js"

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
