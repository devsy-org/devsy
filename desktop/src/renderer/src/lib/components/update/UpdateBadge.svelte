<script lang="ts">
  import { Button } from "$lib/components/ui/button/index.js"
  import { Download, CheckCircle2 } from "@lucide/svelte"
  import { hasUpdate, isReady, updateStatus } from "$lib/stores/updates.svelte.js"

  let { onclick }: { onclick: () => void } = $props()

  const s = $derived(updateStatus())
  const show = $derived(hasUpdate())
  const ready = $derived(isReady())
  const downloading = $derived(s.state === "downloading")
</script>

{#if show}
  <Button
    variant="ghost"
    size="sm"
    {onclick}
    class={"gap-2 " + (ready ? "text-primary" : "text-muted-foreground")}
    title={ready
      ? `Update v${s.version ?? ""} ready`
      : downloading
        ? `Update v${s.version ?? ""} downloading`
        : `Update v${s.version ?? ""} available`}
  >
    {#if ready}
      <CheckCircle2 class="h-4 w-4" />
      <span class="text-xs">Update ready</span>
    {:else if downloading}
      <Download class="h-4 w-4 animate-pulse" />
      <span class="text-xs">{(s.progress?.percent ?? 0).toFixed(0)}%</span>
    {:else}
      <Download class="h-4 w-4" />
      <span class="text-xs">Update available</span>
    {/if}
  </Button>
{/if}
