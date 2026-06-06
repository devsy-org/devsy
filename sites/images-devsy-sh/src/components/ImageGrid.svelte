<script lang="ts">
import type { CatalogImage } from "../lib/catalog-schema"
import ImageCard from "./ImageCard.svelte"

let { images }: { images: CatalogImage[] } = $props()
</script>

{#if images.length === 0}
  <p class="py-12 text-center text-muted-foreground">No images match your search.</p>
{:else}
  <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
    {#each images as image (image.id)}
      <ImageCard {image} />
    {/each}
  </div>
{/if}
