import { randomUUID } from "node:crypto"
import { open, rename, rm } from "node:fs/promises"
import {
  captureNotes,
  checkIndexIdentity,
  checkReadIdentity,
  InvocationTable,
  type InvocationView,
  invocationStateLabel,
  invocationTitle,
  type LogIndexResponse,
  type LogReadResponse,
  type LogRecord,
  type LogRunExportRequest,
  type LogRunReadRequest,
  type LogRunsRequest,
  parseIndexResponse,
  parseReadResponse,
  validateRunExportRequest,
  validateRunReadRequest,
  validateRunsRequest,
} from "../shared/workspace-logs.js"

/** Largest CLI stdout accepted for one page (the Go side caps pages at 256 KiB). */
export const MAX_RESPONSE_BYTES = 1024 * 1024
export const READ_TIMEOUT_MS = 20_000
export const EXPORT_PAGE_LIMIT = 500
export const MAX_EXPORT_PAGES = 400
export const MAX_EXPORT_BYTES = 64 * 1024 * 1024

export type WorkspaceLogErrorCode =
  | "cancelled"
  | "timeout"
  | "contract"
  | "limit"
  | "cli"

export class WorkspaceLogError extends Error {
  constructor(
    readonly code: WorkspaceLogErrorCode,
    message: string,
    readonly cause?: unknown,
  ) {
    super(message)
    this.name = "WorkspaceLogError"
  }
}

export interface WorkspaceLogCli {
  run<T>(
    args: string[],
    options?: { maxBuffer?: number; signal?: AbortSignal },
  ): Promise<T>
}

export interface ExportFs {
  open: typeof open
  rename: typeof rename
  rm: typeof rm
}

export interface WorkspaceLogReaderOptions {
  timeoutMs?: number
  fs?: ExportFs
}

type Slot = "list" | "read" | "export"

/**
 * Small adapter over `workspace logs --source setup`. It keeps no archive of
 * its own. Each slot holds at most one in-flight request, and cancel() stops
 * only these readers, never the workspace's task follower.
 */
export class WorkspaceLogReader {
  private inflight = new Map<Slot, AbortController>()
  private readonly timeoutMs: number
  private readonly fs: ExportFs

  constructor(
    private readonly cli: WorkspaceLogCli,
    options: WorkspaceLogReaderOptions = {},
  ) {
    this.timeoutMs = options.timeoutMs ?? READ_TIMEOUT_MS
    this.fs = options.fs ?? { open, rename, rm }
  }

  /** Cancels every in-flight reader request. Safe when none is active. */
  cancel(): void {
    for (const controller of this.inflight.values()) controller.abort()
    this.inflight.clear()
  }

  async runs(raw: unknown): Promise<LogIndexResponse> {
    const req = this.validated(() => validateRunsRequest(raw))
    const signal = this.begin("list")
    try {
      const out = await this.exec(runsArgs(req), signal)
      try {
        const res = parseIndexResponse(out)
        checkIndexIdentity(req, res)
        return res
      } catch (error) {
        throw contractError(error)
      }
    } finally {
      this.release("list", signal)
    }
  }

  async read(raw: unknown): Promise<LogReadResponse> {
    const req = this.validated(() => validateRunReadRequest(raw))
    const signal = this.begin("read")
    try {
      return await this.readPage(req, signal)
    } finally {
      this.release("read", signal)
    }
  }

  /**
   * Streams bounded pages into a temp file beside the destination and
   * renames it on success. Any failure or cancellation removes the temp file.
   */
  async exportRun(
    raw: unknown,
    destination: string,
  ): Promise<{ bytes: number; gaps: boolean }> {
    const req = this.validated(() => validateRunExportRequest(raw))
    const signal = this.begin("export")
    try {
      const temp = `${destination}.${randomUUID()}.tmp`
      const handle = await this.fs.open(temp, "wx", 0o600)
      try {
        const result = await this.writePages(req, signal, handle)
        await handle.close()
        await this.fs.rename(temp, destination)
        return result
      } catch (error) {
        await handle.close().catch(() => undefined)
        await this.fs.rm(temp, { force: true }).catch(() => undefined)
        throw error
      }
    } finally {
      this.release("export", signal)
    }
  }

  private async writePages(
    req: LogRunExportRequest,
    signal: AbortSignal,
    handle: Awaited<ReturnType<ExportFs["open"]>>,
  ): Promise<{ bytes: number; gaps: boolean }> {
    const formatter = new ExportFormatter()
    let bytes = 0
    let after: string | undefined
    for (let page = 0; page < MAX_EXPORT_PAGES; page++) {
      const res = await this.readPage(
        { ...req, after, limit: EXPORT_PAGE_LIMIT },
        signal,
      )
      bytes += await writeText(handle, formatter.page(res), bytes)
      if (!res.hasMore) {
        bytes += await writeText(handle, formatter.footer(res), bytes)
        return { bytes, gaps: formatter.gaps || !res.complete }
      }
      if (!res.nextCursor || res.nextCursor === after)
        throw new WorkspaceLogError("contract", "Log paging did not advance")
      after = res.nextCursor
    }
    throw new WorkspaceLogError("limit", "Export exceeds the page limit")
  }

  private async readPage(
    req: LogRunReadRequest,
    signal: AbortSignal,
  ): Promise<LogReadResponse> {
    const out = await this.exec(readArgs(req), signal)
    try {
      const res = parseReadResponse(out)
      checkReadIdentity(req, res)
      return res
    } catch (error) {
      throw contractError(error)
    }
  }

  private validated<T>(fn: () => T): T {
    try {
      return fn()
    } catch (error) {
      throw contractError(error)
    }
  }

  /** Replaces any earlier request in the slot, so only one stays in flight. */
  private begin(slot: Slot): AbortSignal {
    this.inflight.get(slot)?.abort()
    const controller = new AbortController()
    this.inflight.set(slot, controller)
    return controller.signal
  }

  private release(slot: Slot, signal: AbortSignal): void {
    if (this.inflight.get(slot)?.signal === signal) this.inflight.delete(slot)
  }

  private async exec(args: string[], signal: AbortSignal): Promise<unknown> {
    const child = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    let onAbort: (() => void) | undefined
    const guard = new Promise<never>((_, reject) => {
      timer = setTimeout(() => {
        child.abort()
        reject(new WorkspaceLogError("timeout", "Log read timed out"))
      }, this.timeoutMs)
      onAbort = () => {
        child.abort()
        reject(new WorkspaceLogError("cancelled", "Log read was cancelled"))
      }
      if (signal.aborted) onAbort()
      else signal.addEventListener("abort", onAbort, { once: true })
    })
    try {
      return await Promise.race([
        this.cli.run<unknown>(args, {
          maxBuffer: MAX_RESPONSE_BYTES,
          signal: child.signal,
        }),
        guard,
      ])
    } catch (error) {
      if (error instanceof WorkspaceLogError) throw error
      throw new WorkspaceLogError("cli", messageOf(error), error)
    } finally {
      clearTimeout(timer)
      if (onAbort) signal.removeEventListener("abort", onAbort)
    }
  }
}

function runsArgs(req: LogRunsRequest): string[] {
  const args = ["workspace", "logs"]
  if (req.workspaceId) args.push(req.workspaceId)
  args.push("--source", "setup", "--list", "--context", req.context)
  if (req.limit) args.push("--limit", String(req.limit))
  if (req.after) args.push("--after", req.after)
  args.push("--result-format", "json")
  return args
}

function readArgs(req: LogRunReadRequest): string[] {
  const args = ["workspace", "logs"]
  if (req.workspaceId) args.push(req.workspaceId)
  args.push("--source", "setup", "--run", req.runId, "--context", req.context)
  if (req.hook) args.push("--hook", req.hook)
  if (req.invocationId) args.push("--invocation", req.invocationId)
  if (req.limit) args.push("--limit", String(req.limit))
  if (req.after) args.push("--after", req.after)
  args.push("--result-format", "json")
  return args
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

function contractError(error: unknown): WorkspaceLogError {
  if (error instanceof WorkspaceLogError) return error
  return new WorkspaceLogError("contract", messageOf(error), error)
}

async function writeText(
  handle: Awaited<ReturnType<ExportFs["open"]>>,
  text: string,
  written: number,
): Promise<number> {
  const size = Buffer.byteLength(text)
  if (written + size > MAX_EXPORT_BYTES)
    throw new WorkspaceLogError("limit", "Export exceeds the size limit")
  if (size > 0) await handle.write(text)
  return size
}

const STREAM_TAG = { stdout: "out", stderr: "err", combined: "out+err" }

/**
 * Renders pages as text, tagging each line with its invocation and stream.
 * Output is written literally. Gaps and unavailable sources are stated in
 * trailing comment lines so a saved file never looks complete when it is not.
 */
class ExportFormatter {
  gaps = false
  private table = new InvocationTable()
  private open: string | undefined
  private started = false

  page(res: LogReadResponse): string {
    this.table.apply(res.records)
    this.table.markSettled(res.complete && !res.hasMore)
    const parts: string[] = []
    if (!this.started) {
      this.started = true
      parts.push(
        `# Devsy retained setup output\n# run ${res.run.runId} (context ${res.run.context})\n`,
      )
    }
    const views = new Map(this.table.list().map((v) => [v.id, v]))
    for (const r of res.records) {
      if (r.kind === "capture") this.gaps = true
      if (r.kind === "output") parts.push(this.outputText(r, views))
    }
    return parts.join("")
  }

  footer(res: LogReadResponse): string {
    const notes = captureNotes(res.run, res.availability, res.complete)
    if (notes.length > 0) this.gaps = true
    const end = this.open ? "\n" : ""
    this.open = undefined
    return `${end}${notes.map((n) => `# ${n}\n`).join("")}`
  }

  private outputText(r: LogRecord, views: Map<string, InvocationView>): string {
    const slot = `${r.invocationId}\u0000${r.stream}`
    const view = views.get(r.invocationId as string)
    const label = view
      ? `${invocationTitle(view)} [${invocationStateLabel(view.state)}]`
      : (r.invocationId as string)
    const tag = `[${label}][${STREAM_TAG[r.stream as keyof typeof STREAM_TAG]}] `
    const text = r.text ?? ""
    if (text === "") return ""
    let out = ""
    if (this.open && this.open !== slot) out += "\n"
    const continuing = this.open === slot
    const lines = text.split("\n")
    lines.forEach((line, i) => {
      const last = i === lines.length - 1
      if (last && line === "") return
      out += (i === 0 && continuing ? "" : tag) + line + (last ? "" : "\n")
    })
    this.open = text.endsWith("\n") ? undefined : slot
    return out
  }
}
