<script lang="ts">
import * as Dialog from "$lib/components/ui/dialog/index.js"
import { Button } from "$lib/components/ui/button/index.js"
import type { Snippet } from "svelte"

let {
  open = $bindable(false),
  title = "Are you sure?",
  description = "",
  confirmLabel = "Confirm",
  cancelLabel = "Cancel",
  variant = "destructive" as "destructive" | "default",
  loading = false,
  onconfirm,
  children,
}: {
  open?: boolean
  title?: string
  description?: string
  confirmLabel?: string
  cancelLabel?: string
  variant?: "destructive" | "default"
  loading?: boolean
  onconfirm: () => void
  children?: Snippet
} = $props()
</script>

<Dialog.Root bind:open>
	<Dialog.Content>
		<Dialog.Header>
			<Dialog.Title>{title}</Dialog.Title>
			{#if description}
				<Dialog.Description>{description}</Dialog.Description>
			{/if}
		</Dialog.Header>

		{#if children}
			{@render children()}
		{/if}

		<Dialog.Footer>
			<Button variant="outline" onclick={() => (open = false)} disabled={loading}>
				{cancelLabel}
			</Button>
			<Button {variant} onclick={onconfirm} disabled={loading}>
				{loading ? "..." : confirmLabel}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
