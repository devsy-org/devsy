<script lang="ts">
import { Switch } from "bits-ui"
import { cn } from "$lib/utils.js"

let {
  ref = $bindable(null),
  checked = $bindable(false),
  class: className,
  ...restProps
}: Switch.RootProps & {
  ref?: HTMLElement | null
} = $props()
</script>

<Switch.Root
  bind:ref
  bind:checked
  class={cn(
    "peer inline-flex h-6 w-11 shrink-0 cursor-pointer items-center rounded-full border-2 border-transparent transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:cursor-not-allowed disabled:opacity-50 data-[state=checked]:bg-primary data-[state=unchecked]:bg-input",
    className,
  )}
  {...restProps}
>
  <Switch.Thumb
    class="pointer-events-none block h-5 w-5 rounded-full bg-background shadow-lg ring-0 transition-transform data-[state=checked]:translate-x-5 data-[state=unchecked]:translate-x-0"
  />
</Switch.Root>
