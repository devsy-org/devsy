<script lang="ts">
import type { CatalogCategory } from "../lib/catalog-schema"

let {
  categories,
  selected = $bindable("all"),
}: { categories: CatalogCategory[]; selected?: string } = $props()

const options = $derived([{ id: "all", label: "All" }, ...categories])
</script>

<div class="flex flex-wrap gap-2">
  {#each options as opt (opt.id)}
    <button
      type="button"
      onclick={() => (selected = opt.id)}
      class="rounded-full border px-3 py-1 text-sm {selected === opt.id
        ? 'border-primary bg-primary text-primary-foreground'
        : 'border-border bg-card text-card-foreground'}"
    >
      {opt.label}
    </button>
  {/each}
</div>
