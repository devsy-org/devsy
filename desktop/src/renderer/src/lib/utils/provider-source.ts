import type { ProviderSource } from "$lib/types/index.js"

export function providerSourceReference(
  source?: ProviderSource,
): string | undefined {
  const reference =
    source?.raw || source?.github || source?.url || source?.file || undefined
  if (!reference || !/^https?:\/\//i.test(reference)) return reference

  try {
    const url = new URL(reference)
    const hiddenParameters =
      url.search || url.hash ? " (parameters hidden)" : ""
    return `${url.origin}${url.pathname}${hiddenParameters}`
  } catch {
    return "URL source"
  }
}
