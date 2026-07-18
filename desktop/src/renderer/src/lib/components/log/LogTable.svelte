<script lang="ts">
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import { parseLogLine } from "$lib/utils/log-parser.js"
import { cn } from "$lib/utils.js"

let {
  lines,
  class: className = "",
  maxHeightClass = "max-h-96",
  follow = false,
  maxLines = 5000,
}: {
  lines: string[]
  class?: string
  maxHeightClass?: string
  follow?: boolean
  /** Hard cap on rendered rows; older lines are dropped from the view. */
  maxLines?: number
} = $props()

let viewport = $state<HTMLDivElement | null>(null)
let pinnedToBottom = $state(true)

// Bound the number of DOM rows regardless of how many lines are passed in, so a
// huge saved log can't create hundreds of thousands of nodes. Offscreen rows
// are skipped by the browser via content-visibility; this cap only guards the
// DOM node count.
let hiddenCount = $derived(Math.max(0, lines.length - maxLines))
let visible = $derived(hiddenCount > 0 ? lines.slice(-maxLines) : lines)

function levelRowClass(level: string | undefined): string {
  if (level === "fatal" || level === "error") return "bg-destructive/5"
  if (level === "warn") return "bg-amber-500/5"
  return ""
}

function onScroll() {
  if (!viewport) return
  const distanceFromBottom =
    viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight
  pinnedToBottom = distanceFromBottom <= 8
}

$effect(() => {
  // Follow the tail as new lines arrive, but only while the user is at the bottom.
  void lines.length
  if (follow && pinnedToBottom && viewport) {
    viewport.scrollTop = viewport.scrollHeight
  }
})
</script>

<div
  bind:this={viewport}
  onscroll={onScroll}
  role="table"
  aria-rowcount={lines.length}
  class={cn("relative overflow-auto rounded-md border", maxHeightClass, className)}
>
  <div
    role="row"
    class="sticky top-0 z-10 grid grid-cols-[5rem_5rem_1fr] gap-2 border-b bg-background px-2 py-1 text-start text-xs font-medium"
  >
    <div role="columnheader">Time</div>
    <div role="columnheader">Level</div>
    <div role="columnheader">Message</div>
  </div>

  {#if hiddenCount > 0}
    <div class="border-b bg-muted/40 px-2 py-1 text-center text-xs text-muted-foreground">
      {hiddenCount.toLocaleString()} earlier {hiddenCount === 1 ? "line" : "lines"} hidden — open the full log to see everything
    </div>
  {/if}

  {#each visible as raw, i (i)}
    {@const line = parseLogLine(raw)}
    <div
      role="row"
      aria-rowindex={i + 1}
      class={cn(
        "grid grid-cols-[5rem_5rem_1fr] items-start gap-2 border-b px-2 py-1 [contain-intrinsic-size:auto_1.75rem] [content-visibility:auto]",
        levelRowClass(line.level),
      )}
    >
      <span role="cell" class="truncate font-mono text-xs text-muted-foreground">{line.time}</span>
      <span role="cell">
        {#if line.level}
          <span
            class={badgeVariants({
              variant:
                line.level === "fatal" || line.level === "error"
                  ? "destructive"
                  : line.level === "warn"
                    ? "outline"
                    : "secondary",
            })}
          >
            {line.level}
          </span>
        {/if}
      </span>
      <span role="cell" class="font-mono text-xs break-words whitespace-pre-wrap">{line.message}</span>
    </div>
  {/each}
</div>
