import { describe, it, expect, vi, beforeEach, afterEach } from "vitest"
import { mkdtemp, rm, writeFile, readFile } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { loadCatalog, __setFetchForTest } from "../image-catalog.js"

const SEED = {
  version: 1,
  categories: [{ id: "languages", label: "Languages" }],
  images: [
    { id: "seed-img", ref: "seed:1", name: "Seed", categories: ["languages"] },
  ],
}
const REMOTE = {
  version: 1,
  categories: [{ id: "languages", label: "Languages" }],
  images: [
    { id: "remote-img", ref: "remote:1", name: "Remote", categories: ["languages"] },
  ],
}

let dir: string
let cachePath: string
let seedPath: string

beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), "catalog-"))
  cachePath = join(dir, "image-catalog.json")
  seedPath = join(dir, "seed.json")
  await writeFile(seedPath, JSON.stringify(SEED))
})

afterEach(async () => {
  __setFetchForTest(undefined)
  await rm(dir, { recursive: true, force: true })
})

describe("loadCatalog", () => {
  it("fetches remote and writes cache when remote succeeds", async () => {
    __setFetchForTest(async () => new Response(JSON.stringify(REMOTE)))
    const result = await loadCatalog({
      url: "https://example/catalog.json",
      cachePath,
      seedPath,
      ttlMs: 1000,
      force: true,
    })
    expect(result.images[0].id).toBe("remote-img")
    const cached = JSON.parse(await readFile(cachePath, "utf8"))
    expect(cached.catalog.images[0].id).toBe("remote-img")
    expect(typeof cached.fetchedAt).toBe("number")
  })

  it("falls back to on-disk cache when remote fails", async () => {
    await writeFile(
      cachePath,
      JSON.stringify({ fetchedAt: Date.now(), catalog: REMOTE }),
    )
    __setFetchForTest(async () => {
      throw new Error("network down")
    })
    const result = await loadCatalog({
      url: "https://example/catalog.json",
      cachePath,
      seedPath,
      ttlMs: 1000,
      force: true,
    })
    expect(result.images[0].id).toBe("remote-img")
  })

  it("falls back to seed when remote fails and no cache exists", async () => {
    __setFetchForTest(async () => {
      throw new Error("network down")
    })
    const result = await loadCatalog({
      url: "https://example/catalog.json",
      cachePath,
      seedPath,
      ttlMs: 1000,
      force: true,
    })
    expect(result.images[0].id).toBe("seed-img")
  })

  it("returns fresh cache without fetching when within TTL and not forced", async () => {
    await writeFile(
      cachePath,
      JSON.stringify({ fetchedAt: Date.now(), catalog: REMOTE }),
    )
    const fetchSpy = vi.fn()
    __setFetchForTest(fetchSpy as unknown as typeof fetch)
    const result = await loadCatalog({
      url: "https://example/catalog.json",
      cachePath,
      seedPath,
      ttlMs: 60_000,
      force: false,
    })
    expect(fetchSpy).not.toHaveBeenCalled()
    expect(result.images[0].id).toBe("remote-img")
  })
})
