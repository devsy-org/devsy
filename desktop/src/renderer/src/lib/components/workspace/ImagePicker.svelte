<script lang="ts">
import { onMount } from "svelte"
import { Input } from "$lib/components/ui/input/index.js"
import { Label } from "$lib/components/ui/label/index.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import LanguageIcon from "$lib/components/workspace/LanguageIcon.svelte"
import {
  imageCatalog,
  loadImageCatalog,
  filterImages,
} from "$lib/stores/imageCatalog.js"

let {
  value = $bindable(""),
  onselect,
}: {
  value: string
  onselect?: (ref: string) => void
} = $props()

let search = $state("")
let category = $state("all")
let customRef = $state("")

onMount(() => {
  if ($imageCatalog.images.length === 0) void loadImageCatalog()
})

let filtered = $derived(filterImages($imageCatalog.images, search, category))

function pick(ref: string) {
  value = ref
  onselect?.(ref)
}
</script>

<div class="space-y-3">
  <div class="flex gap-2">
    <Input
      placeholder="Search images..."
      value={search}
      oninput={(e) => (search = e.currentTarget.value)}
    />
  </div>

  <div class="flex flex-wrap gap-1.5">
    <button
      type="button"
      class={badgeVariants({ variant: category === "all" ? "default" : "outline" })}
      onclick={() => (category = "all")}
    >
      All
    </button>
    {#each $imageCatalog.categories as cat (cat.id)}
      <button
        type="button"
        class={badgeVariants({ variant: category === cat.id ? "default" : "outline" })}
        onclick={() => (category = cat.id)}
      >
        {cat.label}
      </button>
    {/each}
  </div>

  {#if $imageCatalog.loading}
    <p class="text-sm text-muted-foreground">Loading catalog…</p>
  {:else if $imageCatalog.error}
    <p class="text-sm text-destructive">Failed to load catalog: {$imageCatalog.error}</p>
  {/if}

  <div class="max-h-64 overflow-y-auto space-y-1.5">
    {#each filtered as img (img.id)}
      <button
        type="button"
        class="flex w-full items-center gap-3 rounded-lg border p-2.5 text-left transition-colors hover:bg-accent/50
          {value === img.ref ? 'border-primary ring-1 ring-primary' : ''}"
        onclick={() => pick(img.ref)}
      >
        <LanguageIcon name={img.icon ?? img.name} class="h-6 w-6 shrink-0" />
        <div class="flex-1 min-w-0">
          <div class="flex items-center gap-2">
            <span class="text-sm font-medium truncate">{img.name}</span>
            {#if img.featured}
              <span class={badgeVariants({ variant: "secondary" })}>Featured</span>
            {/if}
          </div>
          {#if img.description}
            <div class="text-xs text-muted-foreground truncate">{img.description}</div>
          {/if}
          <div class="text-xs text-muted-foreground font-mono truncate">{img.ref}</div>
        </div>
      </button>
    {/each}
    {#if filtered.length === 0 && !$imageCatalog.loading}
      <p class="text-sm text-muted-foreground py-4 text-center">No images match.</p>
    {/if}
  </div>

  <div class="space-y-1.5 border-t pt-3">
    <Label class="text-sm">Custom image</Label>
    <Input
      placeholder="registry/image:tag"
      value={customRef}
      oninput={(e) => {
        customRef = e.currentTarget.value
        pick(customRef.trim())
      }}
    />
    <p class="text-xs text-muted-foreground">
      Use an image not in the catalog.
    </p>
  </div>
</div>
