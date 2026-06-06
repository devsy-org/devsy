import { describe, expect, it } from "vitest"
import { validateCatalog } from "./validate-catalog"

const OK = {
  version: 1,
  categories: [{ id: "languages", label: "Languages" }],
  images: [
    { id: "py", ref: "img:1", name: "Python", categories: ["languages"] },
  ],
}

describe("validateCatalog", () => {
  it("returns no errors for a valid catalog", () => {
    expect(validateCatalog(OK)).toEqual([])
  })

  it("flags a structurally malformed catalog", () => {
    const errors = validateCatalog({
      version: 1,
      categories: [],
      images: [{ id: "x" }],
    })
    expect(errors.some((e) => e.includes("schema"))).toBe(true)
  })

  it("flags duplicate image ids", () => {
    const dup = {
      ...OK,
      images: [
        { id: "py", ref: "a:1", name: "A", categories: ["languages"] },
        { id: "py", ref: "b:1", name: "B", categories: ["languages"] },
      ],
    }
    expect(validateCatalog(dup).some((e) => e.includes("duplicate"))).toBe(true)
  })

  it("flags an image referencing an unknown category", () => {
    const bad = {
      ...OK,
      images: [{ id: "py", ref: "a:1", name: "A", categories: ["nope"] }],
    }
    expect(
      validateCatalog(bad).some((e) => e.includes("unknown category")),
    ).toBe(true)
  })
})
