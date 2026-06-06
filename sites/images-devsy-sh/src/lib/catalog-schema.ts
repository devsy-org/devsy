export interface CatalogCategory {
  id: string
  label: string
}

export interface CatalogImage {
  id: string
  ref: string
  name: string
  description?: string
  categories: string[]
  icon?: string
  featured?: boolean
}

export interface ImageCatalog {
  version: number
  categories: CatalogCategory[]
  images: CatalogImage[]
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
    (v.description === undefined || typeof v.description === "string") &&
    (v.icon === undefined || typeof v.icon === "string") &&
    (v.featured === undefined || typeof v.featured === "boolean") &&
    Array.isArray(v.categories) &&
    v.categories.every((c) => typeof c === "string")
  )
}

export function isImageCatalog(value: unknown): value is ImageCatalog {
  if (!value || typeof value !== "object") return false
  const v = value as Partial<ImageCatalog>
  return (
    typeof v.version === "number" &&
    Array.isArray(v.categories) &&
    v.categories.every(isCatalogCategory) &&
    Array.isArray(v.images) &&
    v.images.every(isCatalogImage)
  )
}
