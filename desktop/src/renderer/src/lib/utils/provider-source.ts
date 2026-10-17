import type { ProviderSource } from "$lib/types/index.js"

export function providerSourceReference(
  source?: ProviderSource,
): string | undefined {
  return (
    source?.raw || source?.github || source?.url || source?.file || undefined
  )
}
