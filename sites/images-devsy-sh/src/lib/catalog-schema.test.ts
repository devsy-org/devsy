import { describe, expect, it } from "vitest"
import { isImageCatalog } from "./catalog-schema"

const VALID = {
  version: 1,
  categories: [{ id: "languages", label: "Languages" }],
  images: [
    { id: "py", ref: "img:1", name: "Python", categories: ["languages"] },
  ],
}

describe("isImageCatalog", () => {
  it("accepts a well-formed catalog", () => {
    expect(isImageCatalog(VALID)).toBe(true)
  })

  it("rejects non-objects", () => {
    expect(isImageCatalog(null)).toBe(false)
    expect(isImageCatalog("x")).toBe(false)
  })

  it("rejects a category missing label", () => {
    expect(isImageCatalog({ ...VALID, categories: [{ id: "x" }] })).toBe(false)
  })

  it("rejects an image missing required fields", () => {
    expect(
      isImageCatalog({ version: 1, categories: [], images: [{ id: "x" }] }),
    ).toBe(false)
  })

  it("rejects an image whose categories are not all strings", () => {
    const bad = {
      ...VALID,
      images: [{ id: "py", ref: "img:1", name: "Python", categories: [1] }],
    }
    expect(isImageCatalog(bad)).toBe(false)
  })

  it("rejects an image with a mistyped optional field", () => {
    const bad = {
      ...VALID,
      images: [
        {
          id: "py",
          ref: "img:1",
          name: "Python",
          categories: ["languages"],
          featured: "yes",
        },
      ],
    }
    expect(isImageCatalog(bad)).toBe(false)
  })
})
