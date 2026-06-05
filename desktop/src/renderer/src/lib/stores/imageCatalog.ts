import { writable } from "svelte/store"
import type {
  CatalogCategory,
  CatalogImage,
  ImageCatalog,
} from "$lib/types/index.js"
import { imageCatalogGet, imageCatalogRefresh } from "$lib/ipc/commands.js"

type State = {
  images: CatalogImage[]
  categories: CatalogCategory[]
  loading: boolean
  error?: string
}

const initial: State = {
  images: [],
  categories: [],
  loading: false,
  error: undefined,
}

const internal = writable<State>(initial)
export const imageCatalog = { subscribe: internal.subscribe }

function apply(catalog: ImageCatalog): void {
  internal.set({
    images: catalog.images,
    categories: catalog.categories,
    loading: false,
    error: undefined,
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
