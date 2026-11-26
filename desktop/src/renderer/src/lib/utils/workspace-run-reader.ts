import type {
  LogReadResponse,
  LogRecord,
  LogRunReadRequest,
  LogRunSummary,
  LogSourceAvailability,
} from "$shared/workspace-logs.js"

export const PAGE_LIMIT = 500
/** Pages drained per load. The rest stays reachable through export. */
export const MAX_PAGES_PER_LOAD = 40
const POLL_MIN_MS = 2000
const POLL_MAX_MS = 10_000

export interface RunReaderState {
  run?: LogRunSummary
  records: LogRecord[]
  availability?: LogSourceAvailability[]
  complete: boolean
  loading: boolean
  /** Set when more pages exist than the view loads. */
  viewTruncated: boolean
  error?: string
}

export type RunReadRequest = Omit<LogRunReadRequest, "after" | "limit">

export interface RunReaderDeps {
  /** Null means the main process superseded or cancelled the request. */
  read: (request: LogRunReadRequest) => Promise<LogReadResponse | null>
  onChange: (state: RunReaderState) => void
  setTimer?: (fn: () => void, ms: number) => unknown
  clearTimer?: (handle: unknown) => void
}

const emptyState = (): RunReaderState => ({
  records: [],
  complete: false,
  loading: false,
  viewTruncated: false,
})

const TERMINAL = new Set(["succeeded", "failed", "skipped", "canceled"])

/**
 * Pages one run into memory and polls it while work is outstanding. Exactly
 * one read is in flight, and every response is checked against the selection
 * generation so a late answer for a previous run or context is dropped.
 */
export class RunReader {
  private generation = 0
  private inflight = false
  private request?: RunReadRequest
  private cursor?: string
  private timer?: unknown
  private delay = POLL_MIN_MS
  private seen = new Set<string>()
  private invocationStates = new Map<string, string>()
  private state: RunReaderState = emptyState()

  constructor(private deps: RunReaderDeps) {}

  snapshot(): RunReaderState {
    return this.state
  }

  /** Starts a fresh selection. Earlier in-flight work becomes stale. */
  start(request: RunReadRequest): void {
    this.reset()
    this.request = request
    this.state = { ...emptyState(), loading: true }
    this.emit()
    void this.load(this.generation)
  }

  /** Stops polling and invalidates in-flight work. */
  stop(): void {
    this.reset()
    this.request = undefined
  }

  private reset(): void {
    this.generation++
    this.inflight = false
    this.cursor = undefined
    this.delay = POLL_MIN_MS
    this.seen.clear()
    this.invocationStates.clear()
    if (this.timer !== undefined)
      (this.deps.clearTimer ?? clearTimeout)(this.timer as never)
    this.timer = undefined
  }

  private emit(): void {
    this.deps.onChange(this.state)
  }

  private async load(generation: number): Promise<void> {
    const request = this.request
    if (!request || this.inflight) return
    this.inflight = true
    let more = false
    let grew = false
    try {
      for (let page = 0; page < MAX_PAGES_PER_LOAD; page++) {
        const res = await this.deps.read({
          ...request,
          after: this.cursor,
          limit: PAGE_LIMIT,
        })
        if (generation !== this.generation) return
        if (!res) {
          this.finishLoad({ ...this.state, loading: false })
          return
        }
        grew = this.merge(res) || grew
        this.cursor = res.nextCursor ?? this.cursor
        more = res.hasMore
        if (!more) break
      }
    } catch (error) {
      if (generation !== this.generation) return
      this.finishLoad({
        ...this.state,
        loading: false,
        error: error instanceof Error ? error.message : String(error),
      })
      return
    }
    this.finishLoad({
      ...this.state,
      loading: false,
      viewTruncated: more,
      error: undefined,
    })
    if (!more) this.schedule(generation, grew)
  }

  private finishLoad(next: RunReaderState): void {
    this.inflight = false
    this.state = next
    this.emit()
  }

  private merge(res: LogReadResponse): boolean {
    const fresh: LogRecord[] = []
    for (const r of res.records) {
      const key = `${r.producerId}:${r.sequence}`
      if (this.seen.has(key)) continue
      this.seen.add(key)
      fresh.push(r)
      if (r.kind === "invocation" && r.invocationId && r.state)
        this.invocationStates.set(r.invocationId, r.state)
    }
    this.state = {
      ...this.state,
      run: res.run,
      availability: res.availability,
      complete: res.complete,
      records:
        fresh.length > 0
          ? [...this.state.records, ...fresh]
          : this.state.records,
    }
    return fresh.length > 0
  }

  private outstanding(): boolean {
    const run = this.state.run
    if (!run || this.state.complete) return false
    const sources = this.state.availability
    if (sources?.length && sources.every((a) => !a.available)) return false
    if (run.commandState === "running" || run.commandState === "pending")
      return true
    for (const state of this.invocationStates.values())
      if (!TERMINAL.has(state)) return true
    return false
  }

  private schedule(generation: number, grew: boolean): void {
    if (generation !== this.generation || !this.outstanding()) return
    this.delay = grew ? POLL_MIN_MS : Math.min(this.delay * 2, POLL_MAX_MS)
    const set = this.deps.setTimer ?? ((fn, ms) => setTimeout(fn, ms))
    this.timer = set(() => {
      this.timer = undefined
      void this.load(generation)
    }, this.delay)
  }
}

export type TranscriptFocus =
  | { mode: "all" }
  | { mode: "invocation"; id: string }
  | { mode: "failed"; fallback: boolean }

/**
 * Chooses what a deep link shows. A pinned invocation that is not in the run
 * falls back to the run's failed invocations rather than an empty view.
 */
export function resolveFocus(
  views: Array<{ id: string; state: string }>,
  pinnedInvocationId: string | undefined,
  selected: string | undefined,
  isFailure: boolean,
): TranscriptFocus {
  const wanted = selected ?? pinnedInvocationId
  if (wanted && views.some((v) => v.id === wanted))
    return { mode: "invocation", id: wanted }
  if (wanted && !selected) return { mode: "failed", fallback: true }
  if (selected) return { mode: "all" }
  return isFailure && views.some((v) => v.state === "failed")
    ? { mode: "failed", fallback: false }
    : { mode: "all" }
}
