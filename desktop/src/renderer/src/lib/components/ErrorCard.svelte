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
    </div>
  </div>
</div>
