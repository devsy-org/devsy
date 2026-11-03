// Typed contract for retained build and lifecycle history. It mirrors the
// JSON written by `devsy workspace logs --source setup --result-format=json`
// and is deliberately separate from the status envelope (schema v1).

export const WORKSPACE_LOG_SCHEMA_VERSION = 1

export const MAX_READ_LIMIT = 2000
export const MAX_INDEX_LIMIT = 100
export const MAX_CURSOR_BYTES = 8 * 1024

export type LogRecordKind = "producer" | "invocation" | "output" | "capture"
export type LogStream = "stdout" | "stderr" | "combined"
export type InvocationState =
  | "pending"
  | "deferred"
  | "running"
  | "succeeded"
  | "failed"
  | "skipped"
  | "canceled"
  | "completion_unknown"
export type ProducerState = "reserved" | "running" | "terminal" | "unavailable"
export type CommandState =
  | "pending"
  | "running"
  | "succeeded"
  | "failed"
  | "canceled"
  | "unknown"
export type CaptureState =
  | "pending"
  | "recording"
  | "complete"
  | "partial"
  | "unavailable"
export type CaptureCondition =
  | "truncated"
  | "retention_gap"
  | "write_error"
  | "unsupported_source"
  | "source_unavailable"
  | "interrupted"
  | "capacity"
  | "producer_cap"
  | "malformed_record"

export const LIFECYCLE_HOOKS = [
  "initializeCommand",
  "onCreateCommand",
  "updateContentCommand",
  "postCreateCommand",
  "postStartCommand",
  "postAttachCommand",
] as const

const INVOCATION_STATES: readonly string[] = [
  "pending",
  "deferred",
  "running",
  "succeeded",
  "failed",
  "skipped",
  "canceled",
  "completion_unknown",
]
const PRODUCER_STATES: readonly string[] = [
  "reserved",
  "running",
  "terminal",
  "unavailable",
]
const COMMAND_STATES: readonly string[] = [
  "pending",
  "running",
  "succeeded",
  "failed",
  "canceled",
  "unknown",
]
const CAPTURE_STATES: readonly string[] = [
  "pending",
  "recording",
  "complete",
  "partial",
  "unavailable",
]
const CONDITIONS: readonly string[] = [
  "truncated",
  "retention_gap",
  "write_error",
  "unsupported_source",
  "source_unavailable",
  "interrupted",
  "capacity",
  "producer_cap",
  "malformed_record",
]

export interface LogRecord {
  schemaVersion: number
  kind: LogRecordKind
  runId: string
  producerId: string
  sequence: number
  time: string
  parentProducerId?: string
  producerState?: ProducerState
  discoverySealed?: boolean
  invocationId?: string
  phase?: string
  hook?: string
  hookIndex?: number
  hookKey?: string
  location?: "host" | "provider_host" | "container"
  state?: InvocationState
  reason?: string
  durationMillis?: number
  exitCode?: number
  signal?: string
  stream?: LogStream
  text?: string
  bytes?: number
  elided?: boolean
  condition?: CaptureCondition
  detail?: string
  droppedBytes?: number
}

export interface LogRunSummary {
  runId: string
  context: string
  workspaceUid: string | null
  workspaceIdAtStart?: string
  command: string
  taskId?: string
  createdAt: string
  commandState: CommandState
  captureState: CaptureState
  conditions?: CaptureCondition[]
}

export interface LogSourceAvailability {
  producerId: string
  available: boolean
  reason?: string
}

export interface LogReadResponse {
  schemaVersion: number
  run: LogRunSummary
  records: LogRecord[]
  nextCursor?: string
  hasMore: boolean
  availability?: LogSourceAvailability[]
  complete: boolean
}

export interface LogIndexResponse {
  schemaVersion: number
  runs: LogRunSummary[]
  nextCursor?: string
  hasMore: boolean
}

/** Identity the renderer holds for a run. The context is always explicit. */
export interface LogRunIdentity {
  context: string
  runId: string
  /** Stored owner of the run. Null or absent when unresolved or unknown. */
  workspaceUid?: string | null
}

export interface LogRunsRequest {
  context: string
  workspaceId?: string
  workspaceUid?: string
  after?: string
  limit?: number
}

export interface LogRunReadRequest extends LogRunIdentity {
  workspaceId?: string
  hook?: string
  invocationId?: string
  after?: string
  limit?: number
}

export type LogRunExportRequest = Omit<LogRunReadRequest, "after" | "limit">

export type LogRunExportResult =
  | { status: "saved"; path: string; bytes: number; gaps: boolean }
  | { status: "canceled" }

export class WorkspaceLogContractError extends Error {
  constructor(message: string) {
    super(message)
    this.name = "WorkspaceLogContractError"
  }
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/
const CONTEXT = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/
const WORKSPACE_ID = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/
const CURSOR = /^[A-Za-z0-9_\-=.]+$/

export const isRunId = (value: unknown): value is string =>
  typeof value === "string" && UUID.test(value)

export const isLifecycleHook = (value: unknown): boolean =>
  typeof value === "string" &&
  (LIFECYCLE_HOOKS as readonly string[]).includes(value)

function fail(message: string): never {
  throw new WorkspaceLogContractError(message)
}

function requireSelector(
  name: string,
  value: unknown,
  pattern: RegExp,
): string {
  if (typeof value !== "string" || !pattern.test(value)) fail(`invalid ${name}`)
  return value
}

function optionalCursor(value: unknown): string | undefined {
  if (value === undefined || value === "") return undefined
  if (
    typeof value !== "string" ||
    value.length > MAX_CURSOR_BYTES ||
    !CURSOR.test(value)
  )
    fail("invalid cursor")
  return value
}

function optionalLimit(value: unknown, max: number): number | undefined {
  if (value === undefined) return undefined
  if (
    !Number.isInteger(value) ||
    (value as number) < 1 ||
    (value as number) > max
  )
    fail("invalid limit")
  return value as number
}

function asRecord(value: unknown, what: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    fail(`${what} is not an object`)
  return value as Record<string, unknown>
}

/** Validates a renderer request. Throws WorkspaceLogContractError. */
export function validateRunsRequest(value: unknown): LogRunsRequest {
  const v = asRecord(value, "request")
  return {
    context: requireSelector("context", v.context, CONTEXT),
    workspaceId:
      v.workspaceId === undefined
        ? undefined
        : requireSelector("workspaceId", v.workspaceId, WORKSPACE_ID),
    workspaceUid:
      v.workspaceUid === undefined
        ? undefined
        : requireSelector("workspaceUid", v.workspaceUid, /^[\w.-]{1,128}$/),
    after: optionalCursor(v.after),
    limit: optionalLimit(v.limit, MAX_INDEX_LIMIT),
  }
}

export function validateRunExportRequest(value: unknown): LogRunExportRequest {
  const v = asRecord(value, "request")
  const req: LogRunExportRequest = {
    context: requireSelector("context", v.context, CONTEXT),
    runId: requireSelector("runId", v.runId, UUID),
  }
  if (v.workspaceId !== undefined)
    req.workspaceId = requireSelector(
      "workspaceId",
      v.workspaceId,
      WORKSPACE_ID,
    )
  if (v.workspaceUid !== undefined && v.workspaceUid !== null)
    req.workspaceUid = requireSelector(
      "workspaceUid",
      v.workspaceUid,
      /^[\w.-]{1,128}$/,
    )
  if (v.hook !== undefined) {
    if (!isLifecycleHook(v.hook)) fail("invalid hook")
    req.hook = v.hook as string
  }
  if (v.invocationId !== undefined)
    req.invocationId = requireSelector("invocationId", v.invocationId, UUID)
  if (req.hook && req.invocationId)
    fail("hook and invocationId cannot be combined")
  return req
}

export function validateRunReadRequest(value: unknown): LogRunReadRequest {
  const v = asRecord(value, "request")
  return {
    ...validateRunExportRequest(value),
    after: optionalCursor(v.after),
    limit: optionalLimit(v.limit, MAX_READ_LIMIT),
  }
}

function optionalString(v: Record<string, unknown>, key: string): void {
  if (v[key] !== undefined && typeof v[key] !== "string")
    fail(`${key} must be a string`)
}

function optionalNumber(v: Record<string, unknown>, key: string): void {
  if (
    v[key] !== undefined &&
    (typeof v[key] !== "number" || !Number.isFinite(v[key]))
  )
    fail(`${key} must be a number`)
}

function oneOf(value: unknown, allowed: readonly string[], what: string): void {
  if (typeof value !== "string" || !allowed.includes(value))
    fail(`invalid ${what}`)
}

const RECORD_STRINGS = [
  "parentProducerId",
  "invocationId",
  "phase",
  "hook",
  "hookKey",
  "reason",
  "signal",
  "text",
  "detail",
]
const RECORD_NUMBERS = [
  "hookIndex",
  "durationMillis",
  "exitCode",
  "bytes",
  "droppedBytes",
]

function parseRecord(value: unknown): LogRecord {
  const v = asRecord(value, "record")
  if (v.schemaVersion !== WORKSPACE_LOG_SCHEMA_VERSION)
    fail("unsupported record schema version")
  oneOf(v.kind, ["producer", "invocation", "output", "capture"], "record kind")
  if (!isRunId(v.runId)) fail("invalid record runId")
  if (!isRunId(v.producerId)) fail("invalid record producerId")
  if (!Number.isSafeInteger(v.sequence) || (v.sequence as number) < 1)
    fail("invalid record sequence")
  if (typeof v.time !== "string") fail("invalid record time")
  for (const key of RECORD_STRINGS) optionalString(v, key)
  for (const key of RECORD_NUMBERS) optionalNumber(v, key)
  if (v.invocationId !== undefined && !isRunId(v.invocationId))
    fail("invalid record invocationId")
  if (v.kind === "invocation") oneOf(v.state, INVOCATION_STATES, "state")
  if (v.kind === "output") {
    oneOf(v.stream, ["stdout", "stderr", "combined"], "stream")
    if (v.text !== undefined && typeof v.text !== "string")
      fail("text must be a string")
  }
  if (v.producerState !== undefined)
    oneOf(v.producerState, PRODUCER_STATES, "producerState")
  if (v.location !== undefined)
    oneOf(v.location, ["host", "provider_host", "container"], "location")
  if (v.kind === "capture") oneOf(v.condition, CONDITIONS, "condition")
  return v as unknown as LogRecord
}

function parseSummary(value: unknown): LogRunSummary {
  const v = asRecord(value, "run")
  if (!isRunId(v.runId)) fail("invalid run id")
  if (typeof v.context !== "string" || v.context.length === 0)
    fail("invalid run context")
  if (v.workspaceUid !== null && typeof v.workspaceUid !== "string")
    fail("invalid run workspaceUid")
  for (const key of ["workspaceIdAtStart", "taskId"]) optionalString(v, key)
  if (typeof v.command !== "string") fail("invalid run command")
  if (typeof v.createdAt !== "string") fail("invalid run createdAt")
  oneOf(v.commandState, COMMAND_STATES, "commandState")
  oneOf(v.captureState, CAPTURE_STATES, "captureState")
  if (v.conditions !== undefined) {
    if (!Array.isArray(v.conditions)) fail("invalid conditions")
    for (const c of v.conditions) oneOf(c, CONDITIONS, "condition")
  }
  return v as unknown as LogRunSummary
}

function checkPaging(v: Record<string, unknown>): void {
  if (v.schemaVersion !== WORKSPACE_LOG_SCHEMA_VERSION)
    fail("unsupported schema version")
  if (typeof v.hasMore !== "boolean") fail("invalid hasMore")
  if (v.nextCursor !== undefined) optionalCursor(v.nextCursor)
}

/** Parses and checks one read page. Records must be an array, never null. */
export function parseReadResponse(value: unknown): LogReadResponse {
  const v = asRecord(value, "read response")
  checkPaging(v)
  if (typeof v.complete !== "boolean") fail("invalid complete")
  if (!Array.isArray(v.records)) fail("records must be an array")
  const run = parseSummary(v.run)
  const records = v.records.map(parseRecord)
  for (const r of records)
    if (r.runId !== run.runId) fail("record belongs to a different run")
  const availability = v.availability
  if (availability !== undefined) {
    if (!Array.isArray(availability)) fail("invalid availability")
    for (const a of availability) {
      const item = asRecord(a, "availability")
      if (typeof item.producerId !== "string") fail("invalid availability")
      if (typeof item.available !== "boolean") fail("invalid availability")
      optionalString(item, "reason")
    }
  }
  return { ...v, run, records } as unknown as LogReadResponse
}

export function parseIndexResponse(value: unknown): LogIndexResponse {
  const v = asRecord(value, "index response")
  checkPaging(v)
  if (!Array.isArray(v.runs)) fail("runs must be an array")
  return { ...v, runs: v.runs.map(parseSummary) } as unknown as LogIndexResponse
}

/** Rejects a response that answers a different selection than requested. */
export function checkReadIdentity(
  req: LogRunIdentity,
  res: LogReadResponse,
): void {
  if (res.run.runId !== req.runId) fail("response is for a different run")
  if (res.run.context !== req.context)
    fail("response is for a different context")
  if (
    typeof req.workspaceUid === "string" &&
    res.run.workspaceUid !== req.workspaceUid
  )
    fail("response is for a different workspace")
}

export function checkIndexIdentity(
  req: LogRunsRequest,
  res: LogIndexResponse,
): void {
  for (const run of res.runs) {
    if (run.context !== req.context)
      fail("index entry from a different context")
    if (req.workspaceUid && run.workspaceUid !== req.workspaceUid)
      fail("index entry from a different workspace")
  }
}

// ---------------------------------------------------------------------------
// Presentation model shared by main (export) and renderer (list, transcript).

export interface InvocationView {
  id: string
  producerId: string
  phase: string
  hook?: string
  hookIndex?: number
  hookKey?: string
  state: InvocationState
  reason?: string
  exitCode?: number
  signal?: string
  durationMillis?: number
  location?: string
  outputBytes: number
  outputRecords: number
  /** True when output for this invocation may be missing from the archive. */
  uncaptured: boolean
  conditions: CaptureCondition[]
  firstSequence: number
}

const UNCAPTURED_CONDITIONS: readonly CaptureCondition[] = [
  "unsupported_source",
  "source_unavailable",
  "interrupted",
  "retention_gap",
  "write_error",
  "capacity",
  "malformed_record",
]

interface TransitionMark {
  producerId: string
  sequence: number
  time: number
  rank: number
}

function stateRank(state: InvocationState | undefined): number {
  switch (state) {
    case "pending":
      return 0
    case "deferred":
      return 1
    case "running":
      return 2
    case "skipped":
      return 3
    default:
      return 4
  }
}

/**
 * Sequences are only comparable inside one producer. Across producers a
 * terminal state is never replaced by an earlier phase, and equal phases fall
 * back to record time.
 */
function supersedes(prior: TransitionMark, next: TransitionMark): boolean {
  if (prior.producerId === next.producerId)
    return next.sequence < prior.sequence
  if (prior.rank !== next.rank) return next.rank < prior.rank
  return next.time < prior.time
}

const IN_FLIGHT_STATES: readonly InvocationState[] = [
  "pending",
  "deferred",
  "running",
]

/**
 * Folds record pages into per-invocation views. Pages may arrive in several
 * calls and late records may precede earlier ones, so each transition is
 * ordered by (producer, sequence) rather than arrival.
 */
export class InvocationTable {
  private views = new Map<string, InvocationView>()
  private latest = new Map<string, TransitionMark>()
  private producerConditions = new Map<string, Set<CaptureCondition>>()
  private unavailable = new Set<string>()
  private settled = false

  apply(records: LogRecord[]): void {
    for (const r of records) {
      if (r.kind === "invocation" && r.invocationId) this.transition(r)
      else if (r.kind === "output" && r.invocationId) this.output(r)
      else if (r.kind === "capture" && r.condition) this.capture(r)
      else if (r.kind === "producer" && r.producerState === "unavailable")
        this.unavailable.add(r.producerId)
    }
  }

  markUnavailable(producerIds: string[]): void {
    for (const id of producerIds) this.unavailable.add(id)
  }

  /**
   * Declares that every producer of the run has finished. A step that never
   * reached a result is then shown as completion unknown instead of pending,
   * deferred or running forever.
   */
  markSettled(settled: boolean): void {
    this.settled = settled
  }

  list(): InvocationView[] {
    return [...this.views.values()]
      .map((v) => this.finish(v))
      .sort((a, b) => a.firstSequence - b.firstSequence)
  }

  private view(r: LogRecord): InvocationView {
    let v = this.views.get(r.invocationId as string)
    if (!v) {
      v = {
        id: r.invocationId as string,
        producerId: r.producerId,
        phase: r.phase ?? "",
        state: "pending",
        outputBytes: 0,
        outputRecords: 0,
        uncaptured: false,
        conditions: [],
        firstSequence: r.sequence,
      }
      this.views.set(v.id, v)
    }
    v.firstSequence = Math.min(v.firstSequence, r.sequence)
    return v
  }

  private transition(r: LogRecord): void {
    const v = this.view(r)
    const mark: TransitionMark = {
      producerId: r.producerId,
      sequence: r.sequence,
      time: Date.parse(r.time) || 0,
      rank: stateRank(r.state),
    }
    const prior = this.latest.get(v.id)
    if (prior && supersedes(prior, mark)) return
    this.latest.set(v.id, mark)
    v.state = r.state as InvocationState
    v.phase = r.phase ?? v.phase
    v.hook = r.hook ?? v.hook
    v.hookIndex = r.hookIndex ?? v.hookIndex
    v.hookKey = r.hookKey ?? v.hookKey
    v.location = r.location ?? v.location
    v.reason = r.reason
    v.exitCode = r.exitCode
    v.signal = r.signal
    v.durationMillis = r.durationMillis ?? v.durationMillis
  }

  private output(r: LogRecord): void {
    const v = this.view(r)
    v.outputRecords++
    v.outputBytes += r.bytes ?? (r.text ? r.text.length : 0)
    if (r.elided) this.note(v, "capacity")
  }

  private capture(r: LogRecord): void {
    const condition = r.condition as CaptureCondition
    if (r.invocationId) {
      this.note(this.view(r), condition)
      return
    }
    const set = this.producerConditions.get(r.producerId) ?? new Set()
    this.producerConditions.set(r.producerId, set)
    set.add(condition)
  }

  private note(v: InvocationView, condition: CaptureCondition): void {
    if (!v.conditions.includes(condition)) v.conditions.push(condition)
  }

  private finish(v: InvocationView): InvocationView {
    const conditions = [...v.conditions]
    for (const c of this.producerConditions.get(v.producerId) ?? [])
      if (!conditions.includes(c)) conditions.push(c)
    const missing =
      this.unavailable.has(v.producerId) ||
      conditions.some((c) => UNCAPTURED_CONDITIONS.includes(c))
    const state =
      this.settled && IN_FLIGHT_STATES.includes(v.state)
        ? "completion_unknown"
        : v.state
    return { ...v, state, conditions, uncaptured: missing }
  }
}

export type OutputPresence = "has-output" | "empty" | "not-captured" | "none"

/** Distinguishes a silent success from output that was never captured. */
export function outputPresence(v: InvocationView): OutputPresence {
  if (v.outputRecords > 0) return "has-output"
  if (v.state === "skipped" || v.state === "pending" || v.state === "deferred")
    return "none"
  if (v.uncaptured) return "not-captured"
  if (v.state === "running" || v.state === "completion_unknown") return "none"
  return "empty"
}

const STATE_LABEL: Record<InvocationState, string> = {
  pending: "Pending",
  deferred: "Deferred",
  running: "Running",
  succeeded: "Succeeded",
  failed: "Failed",
  skipped: "Skipped",
  canceled: "Canceled",
  completion_unknown: "Completion unknown",
}

export const invocationStateLabel = (state: InvocationState): string =>
  STATE_LABEL[state] ?? state

export function invocationTitle(v: InvocationView): string {
  const base = v.hook ?? (v.phase || "step")
  const key = v.hookKey ? ` (${v.hookKey})` : ""
  const index =
    v.hookIndex !== undefined && v.hookIndex > 0 ? ` #${v.hookIndex + 1}` : ""
  return `${base}${key}${index}`
}

const REASON_LABEL: Record<string, string> = {
  already_attempted: "Already attempted, not run again",
  no_command: "No command configured",
  launch_not_observed: "Launch was not observed",
  already_running: "Another worker was already running",
}

export function invocationDetail(v: InvocationView): string {
  const parts: string[] = []
  if (v.reason)
    parts.push(REASON_LABEL[v.reason] ?? v.reason.replaceAll("_", " "))
  if (v.exitCode !== undefined) parts.push(`exit code ${v.exitCode}`)
  if (v.signal) parts.push(`signal ${v.signal}`)
  if (v.durationMillis !== undefined)
    parts.push(`${(v.durationMillis / 1000).toFixed(1)}s`)
  return parts.join(", ")
}

export function failedInvocations(views: InvocationView[]): InvocationView[] {
  return views.filter((v) => v.state === "failed")
}

export interface TranscriptLine {
  /** Stable key within one builder. */
  key: number
  text: string
  stream: LogStream
  invocationId: string
  label: string
}

/**
 * Turns output records into literal lines. Text is never parsed: JSON, HTML
 * and log-looking content stay as typed. A chunk that ends without a newline
 * continues on the next chunk of the same invocation and stream.
 */
export class TranscriptBuilder {
  private lines: TranscriptLine[] = []
  private open = new Map<string, TranscriptLine>()
  private nextKey = 0

  constructor(private labels: (invocationId: string) => string = () => "") {}

  append(records: LogRecord[]): void {
    for (const r of records) {
      if (r.kind !== "output" || !r.invocationId || !r.stream) continue
      this.appendText(r.invocationId, r.stream, r.text ?? "")
    }
  }

  snapshot(): TranscriptLine[] {
    return this.lines.slice()
  }

  get length(): number {
    return this.lines.length
  }

  private appendText(id: string, stream: LogStream, text: string): void {
    if (text === "") return
    const slot = `${id}\u0000${stream}`
    const parts = text.split("\n")
    parts.forEach((part, i) => {
      const last = i === parts.length - 1
      if (last && part === "") {
        this.open.delete(slot)
        return
      }
      const continuing = i === 0 ? this.open.get(slot) : undefined
      const line = continuing ?? this.newLine(id, stream)
      line.text += part
      if (last) this.open.set(slot, line)
      else this.open.delete(slot)
    })
  }

  private newLine(id: string, stream: LogStream): TranscriptLine {
    const line: TranscriptLine = {
      key: this.nextKey++,
      text: "",
      stream,
      invocationId: id,
      label: this.labels(id),
    }
    this.lines.push(line)
    return line
  }
}

/** Plain text for copy and export. Gaps are stated, never hidden. */
export function transcriptText(
  lines: TranscriptLine[],
  notes: string[] = [],
): string {
  const body = lines.map((l) => l.text).join("\n")
  return notes.length > 0
    ? `${notes.map((n) => `# ${n}`).join("\n")}\n${body}`
    : body
}

export function captureNotes(
  run: LogRunSummary,
  availability: LogSourceAvailability[] | undefined,
  complete: boolean,
): string[] {
  const notes: string[] = []
  for (const c of run.conditions ?? [])
    notes.push(`Capture condition: ${c.replaceAll("_", " ")}`)
  for (const a of availability ?? [])
    if (!a.available)
      notes.push(`Source unavailable: ${a.reason ?? "unknown reason"}`)
  if (!complete) notes.push("History is incomplete")
  return notes
}
