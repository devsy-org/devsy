<script lang="ts">
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import { parseLogLine } from "$lib/utils/log-parser.js"
import { cn } from "$lib/utils.js"

let {
  lines,
  class: className = "",
  maxHeightClass = "max-h-96",
  rowHeight = 28,
  overscan = 12,
  follow = false,
}: {
  lines: string[]
  class?: string
  maxHeightClass?: string
  rowHeight?: number
  overscan?: number
  follow?: boolean
} = $props()

let viewport = $state<HTMLDivElement | null>(null)
let scrollTop = $state(0)
let viewportHeight = $state(0)
let pinnedToBottom = $state(true)

let total = $derived(lines.length)
let totalHeight = $derived(total * rowHeight)

let startIndex = $derived(
  Math.max(0, Math.floor(scrollTop / rowHeight) - overscan),
)
let endIndex = $derived(
  Math.min(
    total,
    Math.ceil((scrollTop + viewportHeight) / rowHeight) + overscan,
  ),
)

let visible = $derived(
  lines.slice(startIndex, endIndex).map((raw, i) => ({
    index: startIndex + i,
    line: parseLogLine(raw),
  })),
)

function onScroll() {
  if (!viewport) return
  scrollTop = viewport.scrollTop
  const distanceFromBottom =
    viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight
  pinnedToBottom = distanceFromBottom <= rowHeight
}

$effect(() => {
  if (!viewport) return
  const observer = new ResizeObserver(() => {
    if (viewport) viewportHeight = viewport.clientHeight
  })
  observer.observe(viewport)
  viewportHeight = viewport.clientHeight
  return () => observer.disconnect()
})

$effect(() => {
  // Follow the tail as new lines arrive, but only while the user is at the bottom.
  void total
  if (follow && pinnedToBottom && viewport) {
    viewport.scrollTop = viewport.scrollHeight
  }
})
</script>

<div
  bind:this={viewport}
  onscroll={onScroll}
  role="table"
  aria-rowcount={total}
  class={cn("relative overflow-auto rounded-md border", maxHeightClass, className)}
>
  <div
    role="row"
    class="sticky top-0 z-10 grid grid-cols-[5rem_6rem_1fr] border-b bg-background text-start font-medium"
    style="height: {rowHeight}px"
  >
    <div role="columnheader" class="flex items-center px-2">Time</div>
    <div role="columnheader" class="flex items-center px-2">Level</div>
    <div role="columnheader" class="flex items-center px-2">Message</div>
  </div>

  <div role="rowgroup" class="relative" style="height: {totalHeight}px">
    {#each visible as row (row.index)}
      <div
        role="row"
        aria-rowindex={row.index + 1}
        class={[
          "absolute grid w-full grid-cols-[5rem_6rem_1fr] items-center border-b",
          row.line.level === "fatal" || row.line.level === "error" ? "bg-destructive/5" : "",
          row.line.level === "warn" ? "bg-amber-500/5" : "",
        ].filter(Boolean).join(" ")}
        style="top: {row.index * rowHeight}px; height: {rowHeight}px"
      >
        <div role="cell" class="truncate px-2 font-mono text-xs text-muted-foreground">{row.line.time}</div>
        <div role="cell" class="px-2">
          {#if row.line.level}
            <span
              class={badgeVariants({
                variant:
                  row.line.level === "fatal" || row.line.level === "error"
                    ? "destructive"
                    : row.line.level === "warn"
                      ? "outline"
                      : "secondary",
              })}
            >
              {row.line.level}
            </span>
          {/if}
        </div>
        <div role="cell" class="truncate px-2 text-sm" title={row.line.message}>{row.line.message}</div>
      </div>
    {/each}
  </div>
</div>
