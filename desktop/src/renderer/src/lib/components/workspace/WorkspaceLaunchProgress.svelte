<script lang="ts">
import { Check, Clock3, Loader2, Minus, Terminal, X } from "@lucide/svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { humanPhase } from "$shared/workspace-operation.js"
import type { WorkspaceLaunchTimeline } from "$lib/utils/workspace-launch-timeline.js"
import type { LaunchConfirmationState } from "$lib/utils/workspace-launch-presentation.js"
import {
  presentLaunchOperation,
  topLevelLaunchOperations,
} from "$lib/utils/workspace-launch-presentation.js"

interface Props {
  workspaceId: string
  provider?: string
  ideLabel?: string
  timeline: WorkspaceLaunchTimeline
  running: boolean
  error?: string
  confirmationState: LaunchConfirmationState
  refreshError?: string
  refreshRetrying?: boolean
  onRetryStatus?: () => void
  elapsedMs: number
  logsAvailable: boolean
  logsOpen: boolean
  onToggleLogs: () => void
}

let {
  workspaceId,
  provider,
  ideLabel,
  timeline,
  running,
  error,
  confirmationState,
  refreshError,
  refreshRetrying = false,
  onRetryStatus,
  elapsedMs,
  logsAvailable,
  logsOpen,
  onToggleLogs,
}: Props = $props()

const visibleOperations = $derived(topLevelLaunchOperations(timeline))
const operationRows = $derived.by(() => {
  const shownErrors = new Set<string>()
  return visibleOperations.map((operation) => {
    const presentation = presentLaunchOperation(operation, timeline, error)
    const hasFailure =
      operation.state === "failed" || presentation.hasFailedDescendant
    if (
      hasFailure &&
      presentation.errorMessage &&
      shownErrors.has(presentation.errorMessage)
    ) {
      return {
        operation,
        presentation: { ...presentation, errorMessage: undefined },
      }
    }
    if (hasFailure && presentation.errorMessage) {
      shownErrors.add(presentation.errorMessage)
    }
    return { operation, presentation }
  })
})
const visibleFailure = $derived(
  operationRows.some(
    ({ operation, presentation }) =>
      operation.state === "failed" || presentation.hasFailedDescendant,
  ),
)

const headline = $derived(
  confirmationState === "confirmed"
    ? "Workspace ready"
    : confirmationState === "confirming"
      ? "Confirming workspace status"
      : confirmationState === "stale"
        ? "Workspace created"
        : confirmationState === "failed"
          ? "Workspace creation failed"
          : "Launching workspace",
)

function duration(value?: number): string | undefined {
  if (value === undefined) return undefined
  return `${(value / 1000).toFixed(1)}s`
}

function elapsed(value: number): string {
  const seconds = Math.floor(value / 1000)
  const minutes = Math.floor(seconds / 60)
  return `${minutes}:${String(seconds % 60).padStart(2, "0")}`
}
</script>

<section class="space-y-5" aria-busy={running || refreshRetrying} aria-label="Workspace launch activity">
  <header class="space-y-3 pr-8">
    <h2
      class="text-xl font-semibold tracking-tight"
      aria-live={confirmationState === "confirming" ? "polite" : undefined}
    >
      {headline}
    </h2>
    <div class="flex flex-wrap items-center gap-x-3 gap-y-2 text-sm">
      <span class="break-all font-medium">{workspaceId}</span>
      {#if provider || ideLabel}
        <span class="inline-flex flex-wrap items-center gap-x-2 gap-y-1 rounded-md bg-muted px-2 py-1 text-xs text-muted-foreground">
          {#if provider}<span class="break-all">{provider}</span>{/if}
          {#if provider && ideLabel}<span aria-hidden="true">·</span>{/if}
          {#if ideLabel}<span class="break-all">{ideLabel}</span>{/if}
        </span>
      {/if}
    </div>
  </header>

  {#if confirmationState === "stale"}
    <div class="space-y-2" role="status" aria-live="polite">
      <p class="text-sm font-medium text-amber-700 dark:text-amber-400">Status may be out of date</p>
      {#if refreshError}
        <Button
          variant="outline"
          size="sm"
          disabled={refreshRetrying}
          onclick={onRetryStatus}
        >
          {refreshRetrying ? "Retrying status…" : "Retry status"}
        </Button>
      {/if}
    </div>
  {/if}

  <div class="space-y-2">
    <div class="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
      <h3 class="font-medium">Launch activity</h3>
      <span class="inline-flex items-center gap-1.5">
        <Clock3 class="h-3.5 w-3.5" aria-hidden="true" />
        Elapsed <span class="tabular-nums">{elapsed(elapsedMs)}</span>
      </span>
    </div>
    {#if operationRows.length}
      <ol class="divide-y divide-border rounded-lg border" aria-label="Workspace lifecycle">
        {#each operationRows as row (row.operation.key)}
          {@const operation = row.operation}
          {@const presentation = row.presentation}
          <li class="flex gap-3 px-4 py-3 first:rounded-t-lg last:rounded-b-lg {operation.state === 'running' ? 'bg-primary/5' : ''}">
            <span class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-muted" aria-hidden="true">
              {#if operation.state === "running"}
                <Loader2 class="h-4 w-4 animate-spin text-primary motion-reduce:animate-none" />
              {:else if operation.state === "succeeded"}
                <Check class="h-4 w-4 text-muted-foreground" />
              {:else if operation.state === "failed"}
                <X class="h-4 w-4 text-destructive" />
              {:else}
                <Minus class="h-4 w-4 text-muted-foreground" />
              {/if}
            </span>
            <div class="min-w-0 flex-1">
              <div class="flex items-baseline justify-between gap-3">
                <span
                  class="break-words text-sm font-medium leading-6"
                  class:text-destructive={operation.state === "failed"}
                  aria-live={operation.state === "running" ? "polite" : undefined}
                >
                  {#if operation.state === "failed"}Failed: {:else if operation.state === "skipped"}Skipped: {/if}
                  {humanPhase(operation.phase) ?? operation.phase}
                </span>
                {#if duration(operation.durationMs)}
                  <span class="shrink-0 text-xs tabular-nums text-muted-foreground">{duration(operation.durationMs)}</span>
                {/if}
              </div>
              {#if presentation.descendants.length}
                <div class="mt-2 space-y-1.5 border-l border-border pl-3">
                  {#each presentation.descendants as descendant (descendant.operation.key)}
                    {@const child = descendant.operation}
                    <p
                      class="break-words text-xs leading-5 {child.state === 'failed' ? 'text-destructive' : 'text-muted-foreground'}"
                      style:margin-left="{Math.min(descendant.depth - 1, 4) * 0.75}rem"
                      aria-live={child.state === "running" ? "polite" : undefined}
                    >
                      {#if child.state === "failed"}Failed: {:else if child.state === "skipped"}Skipped: {:else if child.state === "succeeded"}Completed: {/if}
                      {humanPhase(child.phase) ?? child.phase}{child.step ? ` · ${child.step}` : child.state === "running" ? "…" : ""}
                    </p>
                    {#if child.state === "failed" && child.key === presentation.detail?.key && presentation.errorMessage}
                      <p class="break-words text-sm text-destructive" aria-live="assertive">{presentation.errorMessage}</p>
                    {/if}
                  {/each}
                </div>
              {/if}
              {#if operation.state === "failed" && !presentation.hasFailedDescendant && presentation.errorMessage}
                <p class="break-words text-sm text-destructive" aria-live="assertive">{presentation.errorMessage}</p>
              {/if}
              {#if operation.state === "skipped" && operation.step}
                <p class="text-sm text-muted-foreground">{operation.step}</p>
              {/if}
            </div>
          </li>
        {/each}
      </ol>
    {:else if error}
      <p class="break-words text-sm text-destructive" aria-live="assertive">{error}</p>
    {:else if running}
      <p class="text-sm text-muted-foreground" aria-live="polite">Launching workspace {workspaceId}…</p>
    {/if}
  </div>

  {#if error && !visibleFailure && visibleOperations.length > 0}
    <p class="break-words text-sm text-destructive" aria-live="assertive">{error}</p>
  {/if}

  {#if logsAvailable}
    <Button
      variant="ghost"
      size="sm"
      class="-ml-2 text-muted-foreground"
      aria-expanded={logsOpen}
      onclick={onToggleLogs}
    >
      <Terminal class="h-4 w-4" aria-hidden="true" />
      {logsOpen ? "Hide logs" : "View logs"}
    </Button>
  {/if}
</section>
