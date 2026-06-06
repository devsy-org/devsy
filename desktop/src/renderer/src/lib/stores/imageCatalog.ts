import { writable } from "svelte/store"
import type {
  CatalogCategory,
  CatalogImage,
  CatalogOrigin,
  LoadCatalogResult,
} from "$lib/types/index.js"
import { imageCatalogGet, imageCatalogRefresh } from "$lib/ipc/commands.js"

type State = {
  images: CatalogImage[]
  categories: CatalogCategory[]
  loading: boolean
  error?: string
  origin?: CatalogOrigin
}

const initial: State = {
  images: [],
  categories: [],
  loading: false,
  error: undefined,
  origin: undefined,
}

const internal = writable<State>(initial)
export const imageCatalog = { subscribe: internal.subscribe }

function apply(result: LoadCatalogResult): void {
  internal.set({
    images: result.catalog.images,
    categories: result.catalog.categories,
    loading: false,
    error: undefined,
    origin: result.origin,
  })
}

export async function loadImageCatalog(): Promise<void> {
  internal.update((s) => ({ ...s, loading: true, error: undefined }))
  try {
    apply(await imageCatalogGet())
  } catch (err) {
    internal.update((s) => ({
      ...s,
      loading: false,
      error: err instanceof Error ? err.message : String(err),
    }))
  }
}

export async function refreshImageCatalog(): Promise<void> {
  internal.update((s) => ({ ...s, loading: true, error: undefined }))
  try {
    apply(await imageCatalogRefresh())
  } catch (err) {
    internal.update((s) => ({
      ...s,
      loading: false,
      error: err instanceof Error ? err.message : String(err),
    }))
  }
}

export function resetImageCatalogStore(): void {
  internal.set(initial)
}

/**
 * Returns false when a non-empty platform list excludes the host's platform.
 * An empty list is treated as compatible (multi-arch / unknown).
 */
export function isImageCompatible(
  platforms: string[],
  hostPlatform: string,
): boolean {
  if (platforms.length === 0) return true
  return platforms.includes(hostPlatform)
}

export function filterImages(
  images: CatalogImage[],
  search: string,
  category: string,
): CatalogImage[] {
  const q = search.trim().toLowerCase()
  return images
    .filter((img) => category === "all" || img.categories.includes(category))
    .filter((img) => {
      if (!q) return true
      return (
        img.name.toLowerCase().includes(q) ||
        img.ref.toLowerCase().includes(q) ||
        (img.description?.toLowerCase().includes(q) ?? false)
      )
    })
    .sort((a, b) => {
      const fa = a.featured ? 0 : 1
      const fb = b.featured ? 0 : 1
      if (fa !== fb) return fa - fb
      return a.name.localeCompare(b.name)
    })
}
