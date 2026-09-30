<script lang="ts">
  import { Check, Loader2, Minus, X } from "@lucide/svelte"
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
  <header class="flex items-start justify-between gap-4">
    <div>
      <h2
        class="text-lg font-semibold"
        aria-live={confirmationState === "confirming" ? "polite" : undefined}
      >
        {headline}
      </h2>
      <p class="text-sm text-muted-foreground">{workspaceId}</p>
      {#if provider || ideLabel}
        <p class="text-sm text-muted-foreground">{[provider, ideLabel].filter(Boolean).join(" · ")}</p>
      {/if}
    </div>
    <span class="shrink-0 text-sm tabular-nums text-muted-foreground" aria-label="Elapsed time">{elapsed(elapsedMs)}</span>
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

  {#if operationRows.length}
    <ol class="space-y-3" aria-label="Workspace lifecycle">
      {#each operationRows as row (row.operation.key)}
        {@const operation = row.operation}
        {@const presentation = row.presentation}
        {@const child = presentation.detail}
        <li class="flex gap-3">
          <span class="mt-0.5 shrink-0" aria-hidden="true">
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
            <div class="flex justify-between gap-3">
              <span
                class:font-medium={operation.state === "running"}
                class:text-destructive={operation.state === "failed"}
                aria-live={operation.state === "running" ? "polite" : undefined}
              >
                {#if operation.state === "failed"}Failed: {:else if operation.state === "skipped"}Skipped: {/if}
                {humanPhase(operation.phase) ?? operation.phase}
              </span>
              {#if duration(operation.durationMs)}
                <span class="text-xs text-muted-foreground">{duration(operation.durationMs)}</span>
              {/if}
            </div>
            {#if child}
              <p class="text-sm {child.state === 'failed' ? 'text-destructive' : 'text-muted-foreground'}" aria-live={child.state === "running" ? "polite" : undefined}>
                {child.state === "failed" ? "Failed: " : ""}{humanPhase(child.phase) ?? child.phase}{child.step ? ` · ${child.step}` : child.state === "running" ? "…" : ""}
              </p>
              {#if child.state === "failed" && presentation.errorMessage}
                <p class="text-sm text-destructive" aria-live="assertive">{presentation.errorMessage}</p>
              {/if}
            {/if}
            {#if operation.state === "failed" && !presentation.hasFailedDescendant && presentation.errorMessage}
              <p class="text-sm text-destructive" aria-live="assertive">{presentation.errorMessage}</p>
            {/if}
            {#if operation.state === "skipped" && operation.step}
              <p class="text-sm text-muted-foreground">{operation.step}</p>
            {/if}
          </div>
        </li>
      {/each}
    </ol>
  {:else if error}
    <p class="text-sm text-destructive" aria-live="assertive">{error}</p>
  {:else if running}
    <p class="sr-only" aria-live="polite">Launching workspace {workspaceId}</p>
  {/if}

  {#if error && !visibleFailure && visibleOperations.length > 0}
    <p class="text-sm text-destructive" aria-live="assertive">{error}</p>
  {/if}

  {#if logsAvailable}
    <button
      type="button"
      class="text-sm font-medium text-muted-foreground hover:text-foreground"
      aria-expanded={logsOpen}
      onclick={onToggleLogs}
    >
      {logsOpen ? "Hide logs" : "View logs"}
    </button>
  {/if}
</section>
