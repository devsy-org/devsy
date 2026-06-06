import { readFile, writeFile } from "node:fs/promises"
import type {
  CatalogCategory,
  CatalogImage,
  CatalogOrigin,
  ImageCatalog,
  LoadCatalogResult,
} from "../shared/image-catalog-types.js"

const FETCH_TIMEOUT_MS = 10_000

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

export type { CatalogOrigin, LoadCatalogResult }

let fetchImpl: typeof fetch | undefined
export function __setFetchForTest(fn: typeof fetch | undefined): void {
  fetchImpl = fn
}
function getFetch(): typeof fetch {
  return fetchImpl ?? globalThis.fetch
}

function isCatalogCategory(value: unknown): value is CatalogCategory {
  if (!value || typeof value !== "object") return false
  const v = value as Partial<CatalogCategory>
  return typeof v.id === "string" && typeof v.label === "string"
}

function isCatalogImage(value: unknown): value is CatalogImage {
  if (!value || typeof value !== "object") return false
  const v = value as Partial<CatalogImage>
  return (
    typeof v.id === "string" &&
    typeof v.ref === "string" &&
    typeof v.name === "string" &&
    Array.isArray(v.categories) &&
    v.categories.every((c) => typeof c === "string")
  )
}

function isImageCatalog(value: unknown): value is ImageCatalog {
  if (!value || typeof value !== "object") return false
  const v = value as Partial<ImageCatalog>
  return (
    Array.isArray(v.categories) &&
    v.categories.every(isCatalogCategory) &&
    Array.isArray(v.images) &&
    v.images.every(isCatalogImage)
  )
}

async function readCache(cachePath: string): Promise<CatalogCacheFile | null> {
  let raw: string
  try {
    raw = await readFile(cachePath, "utf8")
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code !== "ENOENT") {
      console.warn("[image-catalog] failed to read cache:", err)
    }
    return null
  }
  try {
    const parsed = JSON.parse(raw) as CatalogCacheFile
    return typeof parsed?.fetchedAt === "number" && isImageCatalog(parsed.catalog)
      ? parsed
      : null
  } catch (err) {
    console.warn("[image-catalog] corrupt cache file, ignoring:", err)
    return null
  }
}

async function readSeed(seedPath: string): Promise<ImageCatalog> {
  try {
    const raw = await readFile(seedPath, "utf8")
    const seed = JSON.parse(raw) as ImageCatalog
    if (!isImageCatalog(seed)) throw new Error("seed malformed")
    return seed
  } catch (err) {
    console.error(
      "[image-catalog] bundled seed unreadable/corrupt (packaging bug):",
      err,
    )
    throw err
  }
}

export async function loadCatalog(
  opts: LoadCatalogOptions,
): Promise<LoadCatalogResult> {
  const cache = await readCache(opts.cachePath)
  const fresh = cache && Date.now() - cache.fetchedAt < opts.ttlMs
  if (cache && fresh && !opts.force) {
    return { catalog: cache.catalog, origin: "cache" }
  }

  try {
    const controller = new AbortController()
    const timeout = setTimeout(() => controller.abort(), FETCH_TIMEOUT_MS)
    let res: Response
    try {
      res = await getFetch()(opts.url, { signal: controller.signal })
    } finally {
      clearTimeout(timeout)
    }
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const catalog = (await res.json()) as ImageCatalog
    if (!isImageCatalog(catalog)) throw new Error("malformed catalog")
    try {
      const toWrite: CatalogCacheFile = { fetchedAt: Date.now(), catalog }
      await writeFile(opts.cachePath, JSON.stringify(toWrite))
    } catch (err) {
      console.warn(
        "[image-catalog] failed to write cache (continuing with remote):",
        err,
      )
    }
    return { catalog, origin: "remote" }
  } catch (err) {
    console.warn("[image-catalog] remote fetch failed, using fallback:", err)
    if (cache) return { catalog: cache.catalog, origin: "cache" }
    return { catalog: await readSeed(opts.seedPath), origin: "seed" }
  }
}
