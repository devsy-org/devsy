<script lang="ts">
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import { Star, Loader2 } from "@lucide/svelte"
import ProviderIcon from "./ProviderIcon.svelte"
import { providerVersions } from "$lib/stores/providerVersions.js"
import { providerJobs } from "$lib/stores/providers.js"
import { providerStatus } from "$lib/utils/provider-status.js"
import { providerSourceReference } from "$lib/utils/provider-source.js"
import type { Provider } from "$lib/types/index.js"

let { provider, onopen }: { provider: Provider; onopen?: () => void } = $props()

let status = $derived(providerStatus(provider, $providerJobs[provider.name]))
</script>

<button
  type="button"
  class="rounded-xl border bg-card min-w-0 p-5 text-left text-card-foreground shadow-sm transition-colors hover:bg-accent/50 w-full relative"
  onclick={() => onopen?.()}
>
  {#if $providerVersions.updates[provider.name]?.updateAvailable}
    <p class="mb-3 text-xs font-medium text-amber-700 dark:text-amber-400">
      Update available: {$providerVersions.updates[provider.name]?.latest}
    </p>
  {/if}
  <div class="flex flex-col gap-3">
    <div class="flex min-w-0 flex-wrap items-center gap-3">
      <ProviderIcon name={provider.name} class="size-8 shrink-0" />
      <h3 class="min-w-0 text-lg font-semibold tracking-tight [overflow-wrap:anywhere]">{provider.name}</h3>
      {#if provider.isDefault}
        <span class="{badgeVariants({ variant: 'default' })} gap-1">
          <Star class="size-3" />
          Default
        </span>
      {/if}
    </div>
    <div class="flex flex-wrap items-center gap-2">
      {#if status.kind === "ready"}
        <span class={badgeVariants({ variant: "secondary" })}>{status.label}</span>
      {:else if status.kind === "busy"}
        <span class="{badgeVariants({ variant: 'outline' })} gap-1">
          <Loader2 class="size-3 animate-spin" />
          {status.label}
        </span>
      {:else if status.kind === "failed"}
        <span
          class={badgeVariants({ variant: "destructive" })}
          title={[status.error, status.errorCode, status.errorHint].filter(Boolean).join(" - ")}
        >
          {status.label}
        </span>
      {:else}
        <span class={badgeVariants({ variant: "destructive" })}>{status.label}</span>
      {/if}
      {#if provider.version}
        <span class="text-xs text-muted-foreground [overflow-wrap:anywhere]">Version {provider.version}</span>
      {/if}
    </div>
  </div>

  {#if provider.deprecated}
    <div class="mt-2">
      <span class={badgeVariants({ variant: "outline" })}>Deprecated built-in</span>
    </div>
  {/if}

  {#if provider.description}
    <p class="mt-3 text-sm leading-6 text-muted-foreground [overflow-wrap:anywhere]">{provider.description}</p>
  {/if}

  {#if providerSourceReference(provider.source)}
    <dl class="mt-4 border-t pt-3 text-xs">
      <dt class="mb-1 text-muted-foreground">Source</dt>
      <dd class="font-mono leading-5 text-muted-foreground [overflow-wrap:anywhere]">{providerSourceReference(provider.source)}</dd>
    </dl>
  {/if}
</button>
