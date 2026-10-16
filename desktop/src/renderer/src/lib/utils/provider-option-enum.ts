import type { ProviderOptionEnumValue } from "$lib/types/index.js"

export interface NormalizedProviderOptionEnum {
  value: string
  label: string
}

export function normalizeProviderOptionEnums(
  values: ProviderOptionEnumValue[] | undefined,
): NormalizedProviderOptionEnum[] {
  return (values ?? []).map((option) => {
    if (typeof option === "string") {
      return { value: option, label: option }
    }

    const value = option.value ?? ""
    return { value, label: option.displayName || value }
  })
}
