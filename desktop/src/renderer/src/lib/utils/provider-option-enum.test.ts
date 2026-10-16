import { describe, expect, it } from "vitest"

import {
  initialProviderOptionValue,
  normalizeProviderOptionEnums,
  providerOptionEnumHasValue,
} from "./provider-option-enum.js"

describe("normalizeProviderOptionEnums", () => {
  it("keeps legacy values and separates structured labels from saved values", () => {
    expect(
      normalizeProviderOptionEnums([
        "steady",
        { value: "burst", displayName: "Burst mode" },
        { value: "fallback" },
        { value: "", displayName: "Unspecified" },
      ]),
    ).toEqual([
      { value: "steady", label: "steady", selectValue: "option-0" },
      { value: "burst", label: "Burst mode", selectValue: "option-1" },
      { value: "fallback", label: "fallback", selectValue: "option-2" },
      { value: "", label: "Unspecified", selectValue: "option-3" },
    ])
  })

  it("treats an omitted enum as empty", () => {
    expect(normalizeProviderOptionEnums(undefined)).toEqual([])
  })

  it("uses non-empty selection tokens without changing raw enum values", () => {
    const options = normalizeProviderOptionEnums([
      { value: "", displayName: "No extra settings" },
      "option-0",
    ])

    expect(
      options.map(({ value, label, selectValue }) => ({
        value,
        label,
        selectValue,
      })),
    ).toEqual([
      {
        value: "",
        label: "No extra settings",
        selectValue: "option-0",
      },
      {
        value: "option-0",
        label: "option-0",
        selectValue: "option-1",
      },
    ])
    expect(options.every((option) => option.selectValue.length > 0)).toBe(true)
    expect(new Set(options.map((option) => option.selectValue)).size).toBe(2)
  })

  it("distinguishes explicit empty values from an unset enum and plain strings", () => {
    expect(
      initialProviderOptionValue({ name: "STORED", value: "", enum: [""] }),
    ).toBe("")
    expect(
      initialProviderOptionValue({ name: "DEFAULT", default: "", enum: [""] }),
    ).toBe("")
    expect(initialProviderOptionValue({ name: "UNSET", enum: [""] })).toBe(
      undefined,
    )
    expect(initialProviderOptionValue({ name: "TEXT" })).toBe("")
    expect(
      initialProviderOptionValue({ name: "ENABLED", default: false }),
    ).toBe("false")
    expect(providerOptionEnumHasValue([""], "")).toBe(true)
    expect(providerOptionEnumHasValue(["fast"], "")).toBe(false)
  })
})
