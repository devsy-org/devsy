import { readFile, writeFile } from "node:fs/promises"
import type { ImageCatalog } from "../shared/image-catalog-types.js"

export interface CatalogCacheFile {
  fetchedAt: number
  catalog: ImageCatalog
}

export interface LoadCatalogOptions {
  url: string
  cachePath: string
  seedPath: string
  ttlMs: number
  force: boolean
}

let fetchImpl: typeof fetch | undefined
export function __setFetchForTest(fn: typeof fetch | undefined): void {
  fetchImpl = fn
}
function getFetch(): typeof fetch {
  return fetchImpl ?? globalThis.fetch
}

async function readCache(cachePath: string): Promise<CatalogCacheFile | null> {
  try {
    const raw = await readFile(cachePath, "utf8")
    const parsed = JSON.parse(raw) as CatalogCacheFile
    if (parsed?.catalog?.images) return parsed
    return null
  } catch {
    return null
  }
}

async function readSeed(seedPath: string): Promise<ImageCatalog> {
  const raw = await readFile(seedPath, "utf8")
  return JSON.parse(raw) as ImageCatalog
}

export async function loadCatalog(
  opts: LoadCatalogOptions,
): Promise<ImageCatalog> {
  const cache = await readCache(opts.cachePath)
  const fresh = cache && Date.now() - cache.fetchedAt < opts.ttlMs
  if (cache && fresh && !opts.force) {
    return cache.catalog
  }

  try {
    const res = await getFetch()(opts.url)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const catalog = (await res.json()) as ImageCatalog
    if (!catalog?.images) throw new Error("malformed catalog")
    const toWrite: CatalogCacheFile = { fetchedAt: Date.now(), catalog }
    await writeFile(opts.cachePath, JSON.stringify(toWrite))
    return catalog
  } catch {
    if (cache) return cache.catalog
    return readSeed(opts.seedPath)
  }
}
