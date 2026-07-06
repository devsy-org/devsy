<script lang="ts">
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import * as Table from "$lib/components/ui/table/index.js"
import { parseLogLine } from "$lib/utils/log-parser.js"

let {
  lines,
  class: className = "",
  maxLines = 5000,
}: { lines: string[]; class?: string; maxLines?: number } = $props()

let truncated = $derived(lines.length > maxLines)
let hiddenCount = $derived(truncated ? lines.length - maxLines : 0)
let visibleLines = $derived(truncated ? lines.slice(lines.length - maxLines) : lines)
let parsed = $derived(visibleLines.map(parseLogLine))
</script>

<Table.Root class="w-full table-fixed {className}">
  <Table.Header class="sticky top-0 z-10 bg-background">
    <Table.Row>
      <Table.Head class="w-20">Time</Table.Head>
      <Table.Head class="w-24">Level</Table.Head>
      <Table.Head class="max-w-0">Message</Table.Head>
    </Table.Row>
  </Table.Header>
  <Table.Body>
    {#if truncated}
      <Table.Row>
        <Table.Cell colspan={3} class="text-center text-xs text-muted-foreground">
          {hiddenCount.toLocaleString()} earlier lines hidden — showing the most recent {maxLines.toLocaleString()}
        </Table.Cell>
      </Table.Row>
    {/if}
    {#each parsed as line, i (i)}
      <Table.Row
        class={[
          line.level === "fatal" || line.level === "error" ? "bg-destructive/5" : "",
          line.level === "warn" ? "bg-amber-500/5" : "",
        ].filter(Boolean).join(" ")}
      >
        <Table.Cell class="font-mono text-xs text-muted-foreground">{line.time}</Table.Cell>
        <Table.Cell>
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
        </Table.Cell>
        <Table.Cell class="text-sm max-w-0 whitespace-normal break-words" title={line.message}>{line.message}</Table.Cell>
      </Table.Row>
    {/each}
  </Table.Body>
</Table.Root>
