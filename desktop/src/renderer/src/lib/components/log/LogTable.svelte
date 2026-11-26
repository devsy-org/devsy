<script lang="ts">
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import { parseLogLine } from "$lib/utils/log-parser.js"
import { cn } from "$lib/utils.js"
import type { TranscriptLine } from "$shared/workspace-logs.js"

let {
  lines,
  transcript = undefined,
  class: className = "",
  maxHeightClass = "max-h-96",
  follow = false,
  maxLines = 5000,
}: {
  lines: string[]
  /** Typed transcript mode: literal text, parseLogLine is never called. */
  transcript?: TranscriptLine[]
  class?: string
  maxHeightClass?: string
  follow?: boolean
  maxLines?: number
} = $props()

let total = $derived(transcript ? transcript.length : lines.length)

let viewport = $state<HTMLDivElement | null>(null)
let pinnedToBottom = $state(true)

let hiddenCount = $derived(Math.max(0, total - maxLines))
let visible = $derived(hiddenCount > 0 ? lines.slice(-maxLines) : lines)
let visibleTranscript = $derived(
  transcript && hiddenCount > 0
    ? transcript.slice(-maxLines)
    : (transcript ?? []),
)

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
  aria-rowcount={total + 1}
  class={cn("relative overflow-auto rounded-md border", maxHeightClass, className)}
>
  <div
    role="row"
    aria-rowindex={1}
    class="sticky top-0 z-10 grid grid-cols-[5rem_5rem_minmax(0,1fr)] gap-2 border-b bg-background px-2 py-1 text-start text-xs font-medium"
  >
    {#if transcript}
      <div role="columnheader">Stream</div>
      <div role="columnheader">Step</div>
      <div role="columnheader">Output</div>
    {:else}
      <div role="columnheader">Time</div>
      <div role="columnheader">Level</div>
      <div role="columnheader">Message</div>
    {/if}
  </div>

  {#if hiddenCount > 0}
    <div class="border-b bg-muted/40 px-2 py-1 text-center text-xs text-muted-foreground">
      {hiddenCount.toLocaleString()} earlier {hiddenCount === 1 ? "line" : "lines"} hidden - open the full log to see everything
    </div>
  {/if}

  {#if transcript}
    {#each visibleTranscript as entry, i (entry.key)}
      <div
        role="row"
        aria-rowindex={hiddenCount + i + 2}
        data-stream={entry.stream}
        class="grid grid-cols-[5rem_5rem_minmax(0,1fr)] items-start gap-2 border-b px-2 py-1 [contain-intrinsic-size:auto_1.75rem] [content-visibility:auto]"
      >
        <span role="cell" class="truncate font-mono text-xs text-muted-foreground">{entry.stream === "combined" ? "out+err" : entry.stream}</span>
        <span role="cell" class="truncate font-mono text-xs text-muted-foreground" title={entry.label}>{entry.label}</span>
        <span role="cell" class="min-w-0 font-mono text-xs whitespace-pre-wrap wrap-anywhere">{entry.text === "" ? "\u00a0" : entry.text}</span>
      </div>
    {/each}
  {:else}
  {#each visible as raw, i (i)}
    {@const line = parseLogLine(raw)}
    <div
      role="row"
      aria-rowindex={hiddenCount + i + 2}
      class={cn(
        "grid grid-cols-[5rem_5rem_minmax(0,1fr)] items-start gap-2 border-b px-2 py-1 [contain-intrinsic-size:auto_1.75rem] [content-visibility:auto]",
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
      <span role="cell" class="min-w-0 font-mono text-xs whitespace-pre-wrap wrap-anywhere">{line.message}</span>
    </div>
  {/each}
  {/if}
</div>
