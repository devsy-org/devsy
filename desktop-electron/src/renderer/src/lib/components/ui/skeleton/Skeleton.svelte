<script lang="ts">
import { cn } from "$lib/utils.js"

let {
  class: className = "",
  ...restProps
}: { class?: string } & Record<string, unknown> = $props()
</script>

<div class={cn("animate-pulse rounded-md bg-muted", className)} {...restProps}></div>
