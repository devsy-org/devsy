<script lang="ts">
import { AlertCircle } from "@lucide/svelte"
import type { CLIError } from "../../../../shared/cli-error.js"

let {
  cliError,
  class: className = "",
}: { cliError: CLIError; class?: string } = $props()
</script>

<div
  class="rounded-lg border border-destructive/40 bg-destructive/5 text-destructive p-4 {className}"
  role="alert"
>
  <div class="flex items-start gap-3">
    <AlertCircle class="size-5 shrink-0 mt-0.5" />
    <div class="flex-1 space-y-2 min-w-0">
      <div class="space-y-0.5">
        <p class="text-sm font-medium leading-tight break-words">
          {cliError.message}
        </p>
        {#if cliError.code && cliError.code !== "UNKNOWN"}
          <p class="text-xs font-mono text-destructive/70">{cliError.code}</p>
        {/if}
      </div>
      {#if cliError.hint}
        <p class="text-sm text-destructive/90">Try: {cliError.hint}</p>
      {/if}
      {#if cliError.context && Object.keys(cliError.context).length > 0}
        <details class="text-xs text-destructive/80">
          <summary class="cursor-pointer font-medium">Context</summary>
          <dl class="mt-1 space-y-0.5 font-mono">
            {#each Object.entries(cliError.context) as [key, value]}
              <div class="flex gap-2 break-words">
                <dt class="shrink-0">{key}:</dt>
                <dd>{value}</dd>
              </div>
            {/each}
          </dl>
        </details>
      {/if}
    </div>
  </div>
</div>
