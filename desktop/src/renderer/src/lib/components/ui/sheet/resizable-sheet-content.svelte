<script lang="ts">
import { Dialog as SheetPrimitive } from "bits-ui"
import { X as XIcon } from "@lucide/svelte"
import { GripVertical as GripVerticalIcon } from "@lucide/svelte"
import type { Snippet } from "svelte"
import SheetPortal from "./sheet-portal.svelte"
import SheetOverlay from "./sheet-overlay.svelte"
import { cn, type WithoutChildrenOrChild } from "$lib/utils.js"
import type { ComponentProps } from "svelte"

let {
  ref = $bindable(null),
  class: className,
  portalProps,
  children,

  minWidth = 400,
  maxWidth = 1200,
  ...restProps
}: WithoutChildrenOrChild<SheetPrimitive.ContentProps> & {
  portalProps?: WithoutChildrenOrChild<ComponentProps<typeof SheetPortal>>
  children: Snippet
  minWidth?: number
  maxWidth?: number
} = $props()

let width = $state(672)
let dragging = $state(false)

function onPointerDown(e: PointerEvent) {
  e.preventDefault()
  dragging = true
  const startX = e.clientX
  const startWidth = width

  function onPointerMove(e: PointerEvent) {
    const delta = startX - e.clientX
    width = Math.min(maxWidth, Math.max(minWidth, startWidth + delta))
  }

  function onPointerUp() {
    dragging = false
    window.removeEventListener("pointermove", onPointerMove)
    window.removeEventListener("pointerup", onPointerUp)
  }

  window.addEventListener("pointermove", onPointerMove)
  window.addEventListener("pointerup", onPointerUp)
}
</script>

<SheetPortal {...portalProps}>
  <SheetOverlay />
  <SheetPrimitive.Content
    bind:ref
    data-slot="sheet-content"
    class={cn(
      "bg-background data-[state=open]:animate-in data-[state=closed]:animate-out fixed z-50 flex flex-col gap-4 shadow-lg transition ease-in-out data-[state=closed]:duration-300 data-[state=open]:duration-500",
      "data-[state=closed]:slide-out-to-end data-[state=open]:slide-in-from-end inset-y-0 end-0 h-full border-s",
      className,
    )}
    style="width: {width}px; max-width: calc(100vw - 2rem);"
    {...restProps}
  >
    <!-- Resize handle -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      class="absolute inset-y-0 start-0 z-10 flex w-2 cursor-col-resize items-center justify-center transition-colors hover:bg-border/50 {dragging ? 'bg-border/50' : ''}"
      onpointerdown={onPointerDown}
    >
      <div class="flex h-8 w-3 items-center justify-center rounded-xs border bg-border">
        <GripVerticalIcon class="size-2.5" />
      </div>
    </div>

    {@render children?.()}
    <SheetPrimitive.Close
      class="ring-offset-background focus-visible:ring-ring absolute end-4 top-4 rounded-xs opacity-70 transition-opacity hover:opacity-100 focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-hidden disabled:pointer-events-none"
    >
      <XIcon class="size-4" />
      <span class="sr-only">Close</span>
    </SheetPrimitive.Close>
  </SheetPrimitive.Content>
</SheetPortal>
