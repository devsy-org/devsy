import type {
  ProviderOption,
  ProviderOptionEnumValue,
} from "$lib/types/index.js"

export interface NormalizedProviderOptionEnum {
  value: string
  label: string
  selectValue: string
}

export function normalizeProviderOptionEnums(
  values: ProviderOptionEnumValue[] | undefined,
): NormalizedProviderOptionEnum[] {
  return (values ?? []).map((option, index) => {
    if (typeof option === "string") {
      return { value: option, label: option, selectValue: `option-${index}` }
    }

    const value = option.value ?? ""
    return {
      value,
      label: option.displayName || value,
      selectValue: `option-${index}`,
    }
  })
}

export function initialProviderOptionValue(
  option: ProviderOption,
): string | undefined {
  if (option.value != null) return String(option.value)
  if (option.default != null) return String(option.default)
  if (option.enum?.length) return undefined
  return ""
}

export function providerOptionEnumHasValue(
  values: ProviderOptionEnumValue[] | undefined,
  value: string,
): boolean {
  return normalizeProviderOptionEnums(values).some(
    (option) => option.value === value,
  )
}
