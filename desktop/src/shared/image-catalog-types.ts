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
