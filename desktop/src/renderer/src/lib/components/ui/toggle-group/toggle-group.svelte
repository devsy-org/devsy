<script lang="ts" module>
import type { VariantProps } from "tailwind-variants"
import type { toggleVariants } from "../toggle/index.js"

export type ToggleGroupContext = VariantProps<typeof toggleVariants>

export const TOGGLE_GROUP_CONTEXT = Symbol("toggle-group")
</script>

<script lang="ts">
	import { ToggleGroup as ToggleGroupPrimitive } from "bits-ui";
	import { setContext } from "svelte";
	import { cn } from "$lib/utils.js";

	let {
		ref = $bindable(null),
		class: className,
		value = $bindable(),
		size = "default",
		variant = "default",
		children,
		...restProps
	}: ToggleGroupPrimitive.RootProps & ToggleGroupContext = $props();

	setContext<ToggleGroupContext>(TOGGLE_GROUP_CONTEXT, {
		get variant() {
			return variant;
		},
		get size() {
			return size;
		},
	});
</script>

<ToggleGroupPrimitive.Root
	bind:ref
	bind:value={value as never}
	data-slot="toggle-group"
	data-variant={variant}
	data-size={size}
	class={cn(
		"group/toggle-group flex w-fit items-center rounded-md data-[variant=outline]:shadow-xs",
		className
	)}
	{...restProps}
>
	{@render children?.()}
</ToggleGroupPrimitive.Root>
