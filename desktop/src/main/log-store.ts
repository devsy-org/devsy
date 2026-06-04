import {
  appendFileSync,
  existsSync,
  mkdirSync,
  readdirSync,
  readFileSync,
  statSync,
  unlinkSync,
  writeFileSync,
} from "node:fs"
import { basename, join } from "node:path"

// safeLogFilename strips any directory components from filename so a caller
// can't traverse out of the per-workspace logs dir with "../" segments.
// Throws when the cleaned name doesn't look like a streaming-log file.
function safeLogFilename(filename: string): string {
  const clean = basename(filename)
  if (clean === "" || clean === "." || clean === "..") {
    throw new Error(`invalid log filename: ${filename}`)
  }
  if (!clean.endsWith(".log")) {
    throw new Error(`invalid log filename: ${filename}`)
  }
  return clean
}

function isReadableDir(path: string): boolean {
  if (!existsSync(path)) return false
  try {
    return statSync(path).isDirectory()
  } catch {
    return false
  }
}

export interface LogEntry {
  workspaceId: string
  filename: string
  createdAt: string
  sizeBytes: number
}

let counter = 0

/**
 * LogStore writes desktop-owned streaming command logs into a directory
 * outside the CLI's workspace state subtree. The CLI's `workspace delete`
 * unlinks ~/.devsy/contexts/<ctx>/workspaces/<id>/ wholesale, so co-locating
 * desktop logs there created a file-deletion race when a still-running
 * desktop child emitted output after the directory was gone.
 */
export class LogStore {
  constructor(private logsDir: string) {}

  private workspaceLogDir(context: string, workspaceId: string): string {
    return join(this.logsDir, "workspaces", context, workspaceId)
  }

  createLogFile(context: string, workspaceId: string): string {
    const dir = this.workspaceLogDir(context, workspaceId)
    mkdirSync(dir, { recursive: true })
    const timestamp = new Date().toISOString().replace(/[:.]/g, "-")
    const suffix = String(counter++).padStart(4, "0")
    const filename = `${timestamp}-${suffix}.log`
    const filePath = join(dir, filename)
    writeFileSync(filePath, "")
    return filePath
  }

  appendLog(logPath: string, line: string): void {
    try {
      appendFileSync(logPath, `${line}\n`)
    } catch (err) {
      // Defensive: the workspace dir under the desktop logs root is owned by
      // this process, but a manual `rm -rf` of the logs tree (or any other
      // out-of-band removal) shouldn't crash the main process.
      if ((err as NodeJS.ErrnoException).code !== "ENOENT") throw err
    }
  }

  readLogByPath(logPath: string): string {
    return readFileSync(logPath, "utf-8")
  }

  listLogs(context: string, workspaceId: string): LogEntry[] {
    const dir = this.workspaceLogDir(context, workspaceId)
    if (!existsSync(dir)) return []

    const entries: LogEntry[] = []
    for (const file of readdirSync(dir)) {
      if (!file.endsWith(".log")) continue
      const filePath = join(dir, file)
      const stat = statSync(filePath)
      entries.push({
        workspaceId,
        filename: file,
        createdAt: stat.birthtime.toISOString(),
        sizeBytes: stat.size,
      })
    }

    entries.sort((a, b) => b.filename.localeCompare(a.filename))
    return entries
  }

  readLog(context: string, workspaceId: string, filename: string): string {
    return readFileSync(
      join(this.workspaceLogDir(context, workspaceId), safeLogFilename(filename)),
      "utf-8",
    )
  }

  deleteLog(context: string, workspaceId: string, filename: string): void {
    unlinkSync(
      join(this.workspaceLogDir(context, workspaceId), safeLogFilename(filename)),
    )
  }

  /**
   * Walks every <logsDir>/workspaces/<ctx>/<id>/ tree and removes log files
   * older than maxAgeDays. Missing trees or non-directory entries are skipped
   * without aborting the rest of the pass.
   */
  prune(maxAgeDays: number): number {
    const root = join(this.logsDir, "workspaces")
    if (!isReadableDir(root)) return 0

    const cutoff = Date.now() - maxAgeDays * 24 * 60 * 60 * 1000
    let removed = 0

    for (const ctx of readdirSync(root)) {
      const wsRoot = join(root, ctx)
      if (!isReadableDir(wsRoot)) continue

      for (const wsDir of readdirSync(wsRoot)) {
        const dir = join(wsRoot, wsDir)
        if (!isReadableDir(dir)) continue

        for (const file of readdirSync(dir)) {
          if (!file.endsWith(".log")) continue
          const filePath = join(dir, file)
          const fileStat = statSync(filePath)
          if (fileStat.birthtimeMs < cutoff) {
            unlinkSync(filePath)
            removed++
          }
        }
      }
    }

    return removed
  }
}
