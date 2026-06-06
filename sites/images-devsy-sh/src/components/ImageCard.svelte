<script lang="ts">
import type { CatalogImage } from "../lib/catalog-schema"

let { image }: { image: CatalogImage } = $props()
let copied = $state(false)

async function copy() {
  await navigator.clipboard.writeText(`docker pull ${image.ref}`)
  copied = true
  setTimeout(() => (copied = false), 1500)
}
</script>

<article class="flex flex-col gap-2 rounded-lg border border-border bg-card p-4 text-card-foreground">
  <header class="flex items-center justify-between gap-2">
    <h3 class="font-semibold">{image.name}</h3>
    {#if image.featured}
      <span class="rounded bg-secondary px-2 py-0.5 text-xs text-secondary-foreground">Featured</span>
    {/if}
  </header>
  {#if image.description}
    <p class="text-sm text-muted-foreground">{image.description}</p>
  {/if}
  <code class="mt-auto truncate rounded bg-muted px-2 py-1 text-xs">{image.ref}</code>
  <button
    type="button"
    onclick={copy}
    class="rounded bg-primary px-3 py-1.5 text-sm text-primary-foreground hover:opacity-90"
  >
    {copied ? "Copied!" : "Copy docker pull"}
  </button>
</article>
