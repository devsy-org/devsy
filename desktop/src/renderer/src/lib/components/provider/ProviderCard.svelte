<script lang="ts">
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import ProviderIcon from "./ProviderIcon.svelte"
import ProviderSheet from "./ProviderSheet.svelte"
import { providerVersions } from "$lib/stores/providerVersions.js"
import type { Provider } from "$lib/types/index.js"

let { provider }: { provider: Provider } = $props()
let sheetOpen = $state(false)

function sourceDisplay(p: Provider): string {
  if (p.source?.github) return p.source.github
  if (p.source?.url) return p.source.url
  if (p.source?.file) return p.source.file
  return ""
}
</script>

<button
  type="button"
  class="rounded-xl border bg-card p-6 text-left text-card-foreground shadow-sm transition-colors hover:bg-accent/50 w-full relative"
  onclick={() => (sheetOpen = true)}
>
  {#if $providerVersions.updates[provider.name]?.updateAvailable}
    <span
      class="absolute top-3 right-3 size-2 rounded-full bg-amber-500"
      title="Update available: {$providerVersions.updates[provider.name]?.latest}"
    />
  {/if}
  <div class="flex items-start justify-between gap-3">
    <div class="flex items-center gap-3 min-w-0">
      <ProviderIcon name={provider.name} class="size-8 shrink-0" />
      <h3 class="text-lg font-semibold truncate">{provider.name}</h3>
      {#if provider.isDefault}
        <span class={badgeVariants({ variant: "default" })}>default</span>
      {/if}
    </div>
    <div class="flex gap-1.5 shrink-0">
      {#if provider.state?.initialized}
        <span class={badgeVariants({ variant: "secondary" })}>initialized</span>
      {:else}
        <span class={badgeVariants({ variant: "destructive" })}>not initialized</span>
      {/if}
      {#if provider.version}
        <span class={badgeVariants({ variant: "outline" })}>{provider.version}</span>
      {/if}
    </div>
  </div>

  {#if provider.description}
    <p class="mt-2 text-sm text-muted-foreground truncate">{provider.description}</p>
  {/if}

  {#if sourceDisplay(provider)}
    <p class="mt-1 text-xs text-muted-foreground truncate">{sourceDisplay(provider)}</p>
  {/if}
</button>

<ProviderSheet {provider} bind:open={sheetOpen} />
