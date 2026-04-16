<script lang="ts">
import { toasts } from "$lib/stores/toasts.js"
</script>

{#if $toasts.length > 0}
  <div class="fixed bottom-4 right-4 z-50 flex flex-col gap-2">
    {#each $toasts as toast (toast.id)}
      <button
        type="button"
        class="flex items-center gap-2 rounded-md border px-4 py-3 text-sm shadow-lg transition-opacity {toast.variant === 'error'
          ? 'border-destructive bg-destructive/10 text-destructive'
          : toast.variant === 'success'
            ? 'border-green-500 bg-green-500/10 text-green-700 dark:text-green-400'
            : 'border-border bg-card text-card-foreground'}"
        onclick={() => toasts.dismiss(toast.id)}
      >
        {toast.message}
      </button>
    {/each}
  </div>
{/if}
