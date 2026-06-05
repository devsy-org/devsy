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
} from "./imageCatalog.js"
import { imageCatalogGet } from "$lib/ipc/commands.js"
import type { CatalogImage } from "$lib/types/index.js"

const IMAGES: CatalogImage[] = [
  { id: "py", ref: "py:1", name: "Python", categories: ["lang"], featured: true },
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
      version: 1,
      categories: [{ id: "lang", label: "Languages" }],
      images: IMAGES,
    })
    await loadImageCatalog()
    const state = get(imageCatalog)
    expect(state.images).toHaveLength(3)
    expect(state.loading).toBe(false)
    expect(state.error).toBeUndefined()
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
    const out = filterImages(IMAGES, "node", "all")
    expect(out.map((i) => i.id)).toEqual(["node"])
  })

  it("filterImages: category filter", () => {
    const out = filterImages(IMAGES, "", "tools")
    expect(out.map((i) => i.id)).toEqual(["tf"])
  })
})
