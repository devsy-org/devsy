import type { CatalogImage } from "./catalog-schema"

export function filterImages(
  images: CatalogImage[],
  query: string,
  category: string,
): CatalogImage[] {
  const q = query.trim().toLowerCase()
  return images
    .filter((img) => category === "all" || img.categories.includes(category))
    .filter((img) => {
      if (!q) return true
      const haystack =
        `${img.name} ${img.ref} ${img.description ?? ""}`.toLowerCase()
      return haystack.includes(q)
    })
    .sort((a, b) => {
      const fa = a.featured ? 0 : 1
      const fb = b.featured ? 0 : 1
      if (fa !== fb) return fa - fb
      return a.name.localeCompare(b.name)
    })
}
