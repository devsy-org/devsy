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

export class LogStore {
  constructor(private baseDir: string) {}

  static defaultPath(): LogStore {
    return new LogStore(join(homedir(), ".devpod", "logs"))
  }

  createLogFile(workspaceId: string): string {
    const dir = join(this.baseDir, workspaceId)
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

  listLogs(workspaceId: string): LogEntry[] {
    const dir = join(this.baseDir, workspaceId)
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

  readLog(workspaceId: string, filename: string): string {
    return readFileSync(join(this.baseDir, workspaceId, filename), "utf-8")
  }

  deleteLog(workspaceId: string, filename: string): void {
    unlinkSync(join(this.baseDir, workspaceId, filename))
  }

  prune(maxAgeDays: number): number {
    if (!existsSync(this.baseDir)) return 0

    const cutoff = Date.now() - maxAgeDays * 24 * 60 * 60 * 1000
    let removed = 0

    for (const wsDir of readdirSync(this.baseDir)) {
      const wsPath = join(this.baseDir, wsDir)
      const wsStat = statSync(wsPath)
      if (!wsStat.isDirectory()) continue

      for (const file of readdirSync(wsPath)) {
        if (!file.endsWith(".log")) continue
        const filePath = join(wsPath, file)
        const fileStat = statSync(filePath)
        if (fileStat.birthtimeMs < cutoff) {
          unlinkSync(filePath)
          removed++
        }
      }
    }

    return removed
  }
}
