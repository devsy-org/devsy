import { describe, expect, it } from "vitest"

import { normalizeProviderOptionEnums } from "./provider-option-enum.js"

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
      { value: "steady", label: "steady" },
      { value: "burst", label: "Burst mode" },
      { value: "fallback", label: "fallback" },
      { value: "", label: "Unspecified" },
    ])
  })

  it("treats an omitted enum as empty", () => {
    expect(normalizeProviderOptionEnums(undefined)).toEqual([])
  })
})
