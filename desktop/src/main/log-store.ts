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
import { homedir } from "node:os"
import { join } from "node:path"

export interface LogEntry {
  workspaceId: string
  filename: string
  createdAt: string
  sizeBytes: number
}

let counter = 0

// LogStore writes streaming command logs into the canonical per-workspace
// directory (~/.devsy/contexts/<ctx>/workspaces/<id>/logs/), so `devsy delete`
// wipes them via os.RemoveAll on the workspace dir. Callers must supply the
// workspace's context for every operation; resolve it from DaemonState
// (workspaceContext) before invoking.
export class LogStore {
  constructor(private devsyHomeDir: string) {}

  static defaultPath(): LogStore {
    return new LogStore(join(homedir(), ".devsy"))
  }

  private workspaceLogDir(context: string, workspaceId: string): string {
    return join(
      this.devsyHomeDir,
      "contexts",
      context,
      "workspaces",
      workspaceId,
      "logs",
    )
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
    appendFileSync(logPath, `${line}\n`)
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
      join(this.workspaceLogDir(context, workspaceId), filename),
      "utf-8",
    )
  }

  deleteLog(context: string, workspaceId: string, filename: string): void {
    unlinkSync(join(this.workspaceLogDir(context, workspaceId), filename))
  }

  // prune walks every <devsyHome>/contexts/<ctx>/workspaces/<id>/logs/ tree
  // and removes log files older than maxAgeDays. Missing trees are skipped.
  prune(maxAgeDays: number): number {
    const contextsRoot = join(this.devsyHomeDir, "contexts")
    if (!existsSync(contextsRoot)) return 0

    const cutoff = Date.now() - maxAgeDays * 24 * 60 * 60 * 1000
    let removed = 0

    for (const ctx of readdirSync(contextsRoot)) {
      const wsRoot = join(contextsRoot, ctx, "workspaces")
      if (!existsSync(wsRoot)) continue

      for (const wsDir of readdirSync(wsRoot)) {
        const logDir = join(wsRoot, wsDir, "logs")
        if (!existsSync(logDir)) continue

        for (const file of readdirSync(logDir)) {
          if (!file.endsWith(".log")) continue
          const filePath = join(logDir, file)
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
