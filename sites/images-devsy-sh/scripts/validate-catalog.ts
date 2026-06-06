import { readFileSync } from "node:fs"
import { isImageCatalog } from "../src/lib/catalog-schema.js"

export function validateCatalog(data: unknown): string[] {
  if (!isImageCatalog(data)) return ["catalog does not match schema"]

  const errors: string[] = []
  const categoryIds = new Set(data.categories.map((c) => c.id))
  const seen = new Set<string>()

  for (const image of data.images) {
    if (seen.has(image.id)) errors.push(`duplicate image id: ${image.id}`)
    seen.add(image.id)
    for (const cat of image.categories) {
      if (!categoryIds.has(cat)) {
        errors.push(`image ${image.id} references unknown category: ${cat}`)
      }
    }
  }
  return errors
}

// CLI entry: `tsx scripts/validate-catalog.ts <path>`
const invokedDirectly =
  process.argv[1] && import.meta.url === `file://${process.argv[1]}`
if (invokedDirectly) {
  const path = process.argv[2]
  if (!path) {
    console.error("usage: validate-catalog <catalog.json>")
    process.exit(2)
  }
  let data: unknown
  try {
    data = JSON.parse(readFileSync(path, "utf8"))
  } catch (err) {
    console.error(`failed to read/parse ${path}:`, err)
    process.exit(1)
  }
  const errors = validateCatalog(data)
  if (errors.length > 0) {
    console.error("catalog validation failed:")
    for (const e of errors) console.error(`  - ${e}`)
    process.exit(1)
  }
  console.log("catalog OK")
}
