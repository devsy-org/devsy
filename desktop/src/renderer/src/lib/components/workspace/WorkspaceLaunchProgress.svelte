<script lang="ts">
  import { Check, Loader2, Minus, X } from "@lucide/svelte"
  import { humanPhase } from "$shared/workspace-operation.js"
  import type { WorkspaceLaunchTimeline, LaunchOperation } from "$lib/utils/workspace-launch-timeline.js"

  interface Props {
    workspaceId: string
    provider?: string
    ideLabel?: string
    timeline: WorkspaceLaunchTimeline
    running: boolean
    success: boolean
    error?: string
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
    success,
    error,
    elapsedMs,
    logsAvailable,
    logsOpen,
    onToggleLogs,
  }: Props = $props()

  const parentOf = (operation: LaunchOperation) =>
    operation.parentOperationId
      ? timeline.operations.find((candidate) => candidate.operationId === operation.parentOperationId)
      : undefined

  const visibleOperations = $derived(
    timeline.operations.filter((operation) => !parentOf(operation)),
  )
  const visibleFailure = $derived(
    visibleOperations.some((operation) =>
      operation.state === "failed" || timeline.operations.some(
        (child) => child.parentOperationId === operation.operationId && child.state === "failed",
      ),
    ),
  )

  function activeChild(parent: LaunchOperation): LaunchOperation | undefined {
    if (!parent.operationId) return undefined
    const children = timeline.operations.filter(
      (operation) =>
        operation.parentOperationId === parent.operationId &&
        (operation.state === "running" || operation.state === "failed"),
    )
    const child = children.at(-1)
    return child?.state === "running" ? activeChild(child) ?? child : child
  }

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

<section class="space-y-5" aria-busy={running} aria-label="Workspace launch activity">
  <header class="flex items-start justify-between gap-4">
    <div>
      <h2 class="text-lg font-semibold">
        {success ? "Workspace ready" : error ? "Workspace creation failed" : "Launching workspace"}
      </h2>
      <p class="text-sm text-muted-foreground">{workspaceId}</p>
      {#if provider || ideLabel}
        <p class="text-sm text-muted-foreground">{[provider, ideLabel].filter(Boolean).join(" · ")}</p>
      {/if}
    </div>
    <span class="shrink-0 text-sm tabular-nums text-muted-foreground" aria-label="Elapsed time">{elapsed(elapsedMs)}</span>
  </header>

  {#if visibleOperations.length}
    <ol class="space-y-3" aria-label="Workspace lifecycle">
      {#each visibleOperations as operation (operation.key)}
        {@const child = activeChild(operation)}
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
              {#if child.state === "failed" && (error || child.error?.message)}
                <p class="text-sm text-destructive" aria-live="assertive">{error ?? child.error?.message}</p>
              {/if}
            {/if}
            {#if operation.state === "failed" && (operation.operationId === undefined || !timeline.operations.some((childOperation) => childOperation.parentOperationId === operation.operationId && childOperation.state === "failed")) && (error || operation.error?.message)}
              <p class="text-sm text-destructive" aria-live="assertive">{error ?? operation.error?.message}</p>
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
