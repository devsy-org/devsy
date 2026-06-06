import { describe, it, expect, vi, beforeEach } from "vitest"
import { get } from "svelte/store"

vi.mock("$lib/ipc/commands.js", () => ({
  imageCatalogGet: vi.fn(),
  imageCatalogRefresh: vi.fn(),
}))

import {
  imageCatalog,
  loadImageCatalog,
  resetImageCatalogStore,
  filterImages,
  isImageCompatible,
} from "./imageCatalog.js"
import { imageCatalogGet } from "$lib/ipc/commands.js"
import type { CatalogImage } from "$lib/types/index.js"

const IMAGES: CatalogImage[] = [
  {
    id: "py",
    ref: "py:1",
    name: "Python",
    description: "Python tooling",
    categories: ["lang"],
    featured: true,
  },
  { id: "node", ref: "node:1", name: "Node.js", categories: ["lang"] },
  { id: "tf", ref: "tf:1", name: "Terraform", categories: ["tools"] },
]

describe("imageCatalog store", () => {
  beforeEach(() => {
    resetImageCatalogStore()
    vi.clearAllMocks()
  })

  it("loads catalog via loadImageCatalog", async () => {
    vi.mocked(imageCatalogGet).mockResolvedValue({
      origin: "remote",
      catalog: {
        version: 1,
        categories: [{ id: "lang", label: "Languages" }],
        images: IMAGES,
      },
    })
    await loadImageCatalog()
    const state = get(imageCatalog)
    expect(state.images).toHaveLength(3)
    expect(state.loading).toBe(false)
    expect(state.error).toBeUndefined()
    expect(state.origin).toBe("remote")
  })

  it("records error on failure", async () => {
    vi.mocked(imageCatalogGet).mockRejectedValue(new Error("boom"))
    await loadImageCatalog()
    const state = get(imageCatalog)
    expect(state.error).toContain("boom")
    expect(state.loading).toBe(false)
  })

  it("filterImages: featured first, then alphabetical", () => {
    const out = filterImages(IMAGES, "", "all")
    expect(out.map((i) => i.id)).toEqual(["py", "node", "tf"])
  })

  it("filterImages: search matches name (case-insensitive)", () => {
    const out = filterImages(IMAGES, "NODE", "all")
    expect(out.map((i) => i.id)).toEqual(["node"])
  })

  it("filterImages: search matches ref", () => {
    const out = filterImages(IMAGES, "tf:1", "all")
    expect(out.map((i) => i.id)).toEqual(["tf"])
  })

  it("filterImages: search matches description without crashing on missing ones", () => {
    const out = filterImages(IMAGES, "tooling", "all")
    expect(out.map((i) => i.id)).toEqual(["py"])
  })

  it("filterImages: category filter", () => {
    const out = filterImages(IMAGES, "", "tools")
    expect(out.map((i) => i.id)).toEqual(["tf"])
  })

  it("isImageCompatible: empty list means compatible", () => {
    expect(isImageCompatible([], "linux/arm64")).toBe(true)
  })

  it("isImageCompatible: matching platform is compatible", () => {
    expect(isImageCompatible(["linux/amd64"], "linux/amd64")).toBe(true)
  })

  it("isImageCompatible: missing host platform is incompatible", () => {
    expect(isImageCompatible(["linux/amd64"], "linux/arm64")).toBe(false)
  })
})
