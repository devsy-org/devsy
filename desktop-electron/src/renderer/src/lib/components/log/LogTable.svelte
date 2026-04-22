<script lang="ts">
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import * as Table from "$lib/components/ui/table/index.js"
import { parseLogLine } from "$lib/utils/log-parser.js"

let { lines, class: className = "" }: { lines: string[]; class?: string } =
  $props()

let parsed = $derived(lines.map(parseLogLine))
</script>

<Table.Root class="w-full table-fixed {className}">
  <Table.Header class="sticky top-0 z-10 bg-background">
    <Table.Row>
      <Table.Head class="w-20">Time</Table.Head>
      <Table.Head class="w-16">Level</Table.Head>
      <Table.Head>Message</Table.Head>
    </Table.Row>
  </Table.Header>
  <Table.Body>
    {#each parsed as line, i (i)}
      <Table.Row
        class={line.level === "fatal" || line.level === "error"
          ? "bg-destructive/5"
          : line.level === "warn"
            ? "bg-amber-500/5"
            : ""}
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
        <Table.Cell class="text-sm truncate" title={line.message}>{line.message}</Table.Cell>
      </Table.Row>
    {/each}
  </Table.Body>
</Table.Root>
