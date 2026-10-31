<script lang="ts">
import { onDestroy } from "svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import LogTable from "$lib/components/log/LogTable.svelte"
import {
  workspaceLogRunCancel,
  workspaceLogRunExport,
  workspaceLogRunRead,
  workspaceLogRuns,
} from "$lib/ipc/commands.js"
import { toasts } from "$lib/stores/toasts.js"
import { extractErrorMessage } from "$lib/utils/error.js"
import { formatTimestamp } from "$lib/utils/time.js"
import {
  RunReader,
  type RunReaderState,
  resolveFocus,
} from "$lib/utils/workspace-run-reader.js"
import {
  captureNotes,
  failedInvocations,
  InvocationTable,
  type InvocationView,
  invocationDetail,
  invocationStateLabel,
  invocationTitle,
  type LogRunSummary,
  outputPresence,
  TranscriptBuilder,
  transcriptText,
} from "$shared/workspace-logs.js"

let {
  context,
  workspaceId = undefined,
  workspaceUid = undefined,
  pinnedRunId = undefined,
  pinnedInvocationId = undefined,
  operationRunId = undefined,
  readOnly = false,
}: {
  context: string
  workspaceId?: string
  /** Owner UID of the live workspace. Runs with a different UID are not listed. */
  workspaceUid?: string
  pinnedRunId?: string
  pinnedInvocationId?: string
  /** Run of the operation in progress. It never replaces an explicit choice. */
  operationRunId?: string
  /** Known-run view: stored identity only, no live workspace binding. */
  readOnly?: boolean
} = $props()

let runs = $state<LogRunSummary[]>([])
let runsError = $state<string | undefined>()
let runsLoaded = $state(false)
let selectedRunId = $state<string | undefined>()
let userSelected = $state(false)
let selectedInvocation = $state<string | undefined>()
let exporting = $state(false)
let reader = $state<RunReaderState>({
  records: [],
  complete: false,
  loading: false,
  viewTruncated: false,
})

const runReader = new RunReader({
  read: workspaceLogRunRead,
  onChange: (next) => {
    reader = next
  },
})
let runsGeneration = 0

let liveWorkspaceId = $derived(readOnly ? undefined : workspaceId)
let liveWorkspaceUid = $derived(readOnly ? undefined : workspaceUid)

async function loadRuns() {
  const generation = ++runsGeneration
  try {
    const res = await workspaceLogRuns({
      context,
      workspaceId: liveWorkspaceId,
      workspaceUid: liveWorkspaceUid,
    })
    if (generation !== runsGeneration || !res) return
    runs = res.runs
    runsError = undefined
  } catch (error) {
    if (generation !== runsGeneration) return
    runsError = extractErrorMessage(error)
  } finally {
    if (generation === runsGeneration) runsLoaded = true
  }
}

$effect(() => {
  void context
  void liveWorkspaceId
  void liveWorkspaceUid
  void operationRunId
  runsLoaded = false
  void loadRuns()
})

// A deep link wins on entry, then the latest run. An explicit pick is kept.
$effect(() => {
  if (userSelected && selectedRunId) return
  selectedRunId = pinnedRunId ?? runs[0]?.runId
})

$effect(() => {
  const runId = selectedRunId
  if (!runId) {
    runReader.stop()
    reader = {
      records: [],
      complete: false,
      loading: false,
      viewTruncated: false,
    }
    return
  }
  runReader.start({
    context,
    runId,
    workspaceId: liveWorkspaceId,
    workspaceUid: liveWorkspaceUid,
  })
})

onDestroy(() => {
  runReader.stop()
  // Only history readers: the workspace task follower must keep running.
  void workspaceLogRunCancel().catch(() => undefined)
})

let views = $derived.by(() => {
  const table = new InvocationTable()
  table.apply(reader.records)
  table.markUnavailable(
    (reader.availability ?? [])
      .filter((a) => !a.available)
      .map((a) => a.producerId),
  )
  return table.list()
})

// Wait for the first page so a missing step is not reported while loading.
let focus = $derived(
  reader.run && !reader.loading
    ? resolveFocus(
        views,
        selectedRunId === pinnedRunId ? pinnedInvocationId : undefined,
        selectedInvocation,
        selectedRunId === pinnedRunId && !!pinnedRunId,
      )
    : { mode: "all" as const },
)

let shownIds = $derived.by(() => {
  if (focus.mode === "invocation") return new Set([focus.id])
  if (focus.mode === "failed")
    return new Set(failedInvocations(views).map((v) => v.id))
  return undefined
})

let transcript = $derived.by(() => {
  const titles = new Map(views.map((v) => [v.id, invocationTitle(v)]))
  const builder = new TranscriptBuilder((id) => titles.get(id) ?? "")
  builder.append(
    shownIds
      ? reader.records.filter(
          (r) => r.invocationId && shownIds.has(r.invocationId),
        )
      : reader.records,
  )
  return builder.snapshot()
})

let notes = $derived(
  reader.run
    ? captureNotes(reader.run, reader.availability, reader.complete)
    : [],
)

let focusView = $derived(
  focus.mode === "invocation"
    ? views.find((v) => v.id === focus.id)
    : undefined,
)

function presenceMessage(view: InvocationView): string | undefined {
  const presence = outputPresence(view)
  if (presence === "empty")
    return "Completed with no output (captured, 0 bytes)."
  if (presence === "not-captured")
    return "Output was not captured for this step."
  if (view.state === "skipped")
    return invocationDetail(view) || "This step was skipped."
  return undefined
}

function stateVariant(state: InvocationView["state"]) {
  if (state === "failed") return "destructive"
  if (state === "succeeded") return "default"
  return "secondary"
}

function runLabel(run: LogRunSummary): string {
  return `${formatTimestamp(run.createdAt)} - ${run.command} - ${run.commandState}`
}

function selectRun(event: Event) {
  userSelected = true
  selectedInvocation = undefined
  selectedRunId = (event.currentTarget as HTMLSelectElement).value
}

function selectStep(view: InvocationView) {
  selectedInvocation =
    selectedInvocation === view.id && focus.mode === "invocation"
      ? undefined
      : view.id
}

async function copy() {
  try {
    await navigator.clipboard.writeText(transcriptText(transcript, notes))
    toasts.success("Copied to clipboard")
  } catch {
    toasts.error("Failed to copy to clipboard")
  }
}

async function exportRun() {
  if (!selectedRunId || exporting) return
  exporting = true
  try {
    const result = await workspaceLogRunExport({
      context,
      runId: selectedRunId,
      workspaceId: liveWorkspaceId,
      workspaceUid: liveWorkspaceUid,
      invocationId: focus.mode === "invocation" ? focus.id : undefined,
    })
    if (result.status === "saved")
      toasts.success(
        result.gaps
          ? "Saved. The file notes gaps in the retained history."
          : "Saved retained output",
      )
  } catch (error) {
    toasts.error(`Export failed: ${extractErrorMessage(error)}`)
  } finally {
    exporting = false
  }
}
</script>

<section aria-label="Setup and build history" class="space-y-3" data-testid="workspace-run-logs">
  {#if runsError}
    <p class="rounded-md border p-3 text-sm text-destructive" role="alert">
      Could not read setup history: {runsError}
    </p>
  {/if}

  {#if runsLoaded && runs.length === 0 && !pinnedRunId && !runsError}
    <p class="rounded-md border p-4 text-sm text-muted-foreground">
      No retained setup history for this workspace yet. Output from builds and lifecycle hooks is kept here after the next run.
    </p>
  {:else if selectedRunId}
    <div class="flex flex-wrap items-center gap-2">
      <label class="text-xs text-muted-foreground" for="workspace-run-select">Run</label>
      <select
        id="workspace-run-select"
        class="min-w-0 max-w-full rounded-md border bg-background px-2 py-1 text-sm"
        value={selectedRunId}
        onchange={selectRun}
      >
        {#each runs as run (run.runId)}
          <option value={run.runId}>{runLabel(run)}</option>
        {/each}
        {#if !runs.some((run) => run.runId === selectedRunId)}
          <option value={selectedRunId}>Run {selectedRunId.slice(0, 8)}</option>
        {/if}
      </select>
      {#if reader.run}
        <span class={badgeVariants({ variant: reader.run.commandState === "failed" ? "destructive" : "secondary" })}>
          Command {reader.run.commandState}
        </span>
        <span class={badgeVariants({ variant: "outline" })}>Capture {reader.run.captureState}</span>
      {/if}
      {#if readOnly && reader.run}
        <span class="text-xs text-muted-foreground">
          Read-only history{reader.run.workspaceIdAtStart ? ` for ${reader.run.workspaceIdAtStart}` : ""}
        </span>
      {/if}
      <div class="ml-auto flex gap-1">
        <Button variant="outline" size="sm" onclick={copy} disabled={transcript.length === 0}>Copy</Button>
        <Button variant="outline" size="sm" onclick={exportRun} disabled={exporting}>
          {exporting ? "Exporting..." : "Export"}
        </Button>
      </div>
    </div>

    {#if reader.error}
      <p class="rounded-md border p-3 text-sm text-destructive" role="alert">{reader.error}</p>
    {/if}

    {#each notes as note (note)}
      <p class="text-xs text-muted-foreground">{note}</p>
    {/each}

    {#if views.length > 0}
      <ul class="divide-y rounded-md border" aria-label="Build and lifecycle steps">
        {#each views as view (view.id)}
          <li>
            <button
              type="button"
              class="flex w-full flex-wrap items-center gap-2 px-3 py-2 text-left text-sm hover:bg-muted/50 {focus.mode === 'invocation' && focus.id === view.id ? 'bg-muted/60' : ''}"
              aria-pressed={focus.mode === "invocation" && focus.id === view.id}
              onclick={() => selectStep(view)}
            >
              <span class="font-mono text-xs">{invocationTitle(view)}</span>
              <span class={badgeVariants({ variant: stateVariant(view.state) })}>{invocationStateLabel(view.state)}</span>
              <span class="text-xs text-muted-foreground">{invocationDetail(view)}</span>
              {#if outputPresence(view) === "not-captured"}
                <span class="text-xs text-muted-foreground">output not captured</span>
              {:else if outputPresence(view) === "empty"}
                <span class="text-xs text-muted-foreground">no output</span>
              {/if}
              <span class="ml-auto text-xs text-primary">View output</span>
            </button>
          </li>
        {/each}
      </ul>
    {/if}

    {#if focus.mode === "failed" && focus.fallback}
      <p class="text-xs text-muted-foreground">
        The linked step is not in this run. Showing its failed steps instead.
      </p>
    {/if}

    {#if focusView && presenceMessage(focusView) && transcript.length === 0}
      <p class="rounded-md border p-4 text-sm text-muted-foreground">{presenceMessage(focusView)}</p>
    {:else if transcript.length === 0}
      <p class="rounded-md border p-4 text-sm text-muted-foreground">
        {reader.loading ? "Loading..." : "No output retained for this selection."}
      </p>
    {:else}
      <LogTable lines={[]} {transcript} follow maxHeightClass="max-h-96" />
    {/if}

    {#if reader.viewTruncated}
      <p class="text-xs text-muted-foreground">
        Only the first part of this run is shown. Export to save everything that was retained.
      </p>
    {/if}
  {/if}
</section>
