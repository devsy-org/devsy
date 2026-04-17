<script lang="ts">
import { Toaster as Sonner, type ToasterProps } from "svelte-sonner"

let { ...restProps }: ToasterProps = $props()
</script>

<Sonner
  class="toaster group"
  style="--normal-bg: var(--color-popover); --normal-text: var(--color-popover-foreground); --normal-border: var(--color-border); --success-bg: var(--color-popover); --success-text: var(--color-popover-foreground); --success-border: var(--color-border); --error-bg: var(--color-destructive); --error-text: white; --error-border: var(--color-destructive);"
  {...restProps}
/>
