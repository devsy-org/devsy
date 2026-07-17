<script lang="ts">
	import { ToggleGroup as ToggleGroupPrimitive } from "bits-ui";
	import { getContext } from "svelte";
	import { toggleVariants } from "../toggle/index.js";
	import {
		type ToggleGroupContext,
		TOGGLE_GROUP_CONTEXT,
	} from "./toggle-group.svelte";
	import { cn } from "$lib/utils.js";

	let {
		ref = $bindable(null),
		class: className,
		variant,
		size,
		...restProps
	}: ToggleGroupPrimitive.ItemProps & ToggleGroupContext = $props();

	const ctx = getContext<ToggleGroupContext>(TOGGLE_GROUP_CONTEXT);
</script>

<ToggleGroupPrimitive.Item
	bind:ref
	data-slot="toggle-group-item"
	data-variant={ctx?.variant ?? variant}
	data-size={ctx?.size ?? size}
	class={cn(
		toggleVariants({ variant: ctx?.variant ?? variant, size: ctx?.size ?? size }),
		"min-w-0 flex-1 shrink-0 rounded-none shadow-none first:rounded-l-md last:rounded-r-md focus:z-10 focus-visible:z-10 data-[variant=outline]:border-l-0 data-[variant=outline]:first:border-l",
		className
	)}
	{...restProps}
/>
