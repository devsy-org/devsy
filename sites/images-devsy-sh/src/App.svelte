<script lang="ts">
import catalog from "./catalog.json"
import type { ImageCatalog } from "./lib/catalog-schema"
import { filterImages } from "./lib/filter"
import SearchBar from "./components/SearchBar.svelte"
import CategoryFilter from "./components/CategoryFilter.svelte"
import ImageGrid from "./components/ImageGrid.svelte"

const data = catalog as ImageCatalog
let query = $state("")
let category = $state("all")

const visible = $derived(filterImages(data.images, query, category))
</script>

<main class="mx-auto max-w-5xl px-4 py-10">
  <header class="mb-8">
    <h1 class="text-2xl font-bold">Devsy Images</h1>
    <p class="text-muted-foreground">
      Curated dev container images for the Devsy workspace wizard.
    </p>
    <a href="https://devsy.sh" class="text-sm text-primary underline">devsy.sh</a>
  </header>

  <div class="mb-4 flex flex-col gap-3">
    <SearchBar bind:value={query} />
    <CategoryFilter categories={data.categories} bind:selected={category} />
  </div>

  <ImageGrid images={visible} />
</main>
