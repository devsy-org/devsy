import { describe, expect, it } from "vitest"
import type { CatalogImage } from "./catalog-schema"
import { filterImages } from "./filter"

const IMAGES: CatalogImage[] = [
  {
    id: "py",
    ref: "mcr/python:3.12",
    name: "Python 3.12",
    description: "Python image",
    categories: ["languages"],
  },
  {
    id: "uni",
    ref: "mcr/universal:2",
    name: "Universal",
    description: "Multi-language",
    categories: ["universal"],
    featured: true,
  },
  {
    id: "go",
    ref: "mcr/go:1",
    name: "Go",
    description: "Go image",
    categories: ["languages"],
    featured: true,
  },
]

describe("filterImages", () => {
  it("returns all images sorted featured-first when no query/category", () => {
    const out = filterImages(IMAGES, "", "all")
    expect(out.map((i) => i.id)).toEqual(["go", "uni", "py"])
  })

  it("filters by category", () => {
    const out = filterImages(IMAGES, "", "universal")
    expect(out.map((i) => i.id)).toEqual(["uni"])
  })

  it("matches query against name case-insensitively", () => {
    expect(filterImages(IMAGES, "python", "all").map((i) => i.id)).toEqual([
      "py",
    ])
  })

  it("matches query against ref", () => {
    expect(filterImages(IMAGES, "universal:2", "all").map((i) => i.id)).toEqual(
      ["uni"],
    )
  })

  it("matches query against description", () => {
    expect(
      filterImages(IMAGES, "multi-language", "all").map((i) => i.id),
    ).toEqual(["uni"])
  })

  it("returns empty when nothing matches", () => {
    expect(filterImages(IMAGES, "zzz", "all")).toEqual([])
  })
})
