import pty from "node-pty"
import type { IPty } from "node-pty"
import { platform } from "node:os"
import type { BrowserWindow } from "electron"

interface PtyDeps {
  binaryPath: string
  getMainWindow: () => BrowserWindow | null
}

export class PtyManager {
  private sessions = new Map<string, IPty>()

  constructor(private deps: PtyDeps) {}

  createSession(cols: number, rows: number): string {
    const shell =
      platform() === "win32"
        ? "powershell.exe"
        : process.env.SHELL || "/bin/sh"

    const sessionId = crypto.randomUUID()
    const proc = pty.spawn(shell, [], {
      name: "xterm-256color",
      cols,
      rows,
      env: { ...process.env, TERM: "xterm-256color" },
    })

    this.wire(sessionId, proc)
    return sessionId
  }

  createSshSession(workspaceId: string, cols: number, rows: number): string {
    const sessionId = crypto.randomUUID()
    const proc = pty.spawn(this.deps.binaryPath, ["ssh", workspaceId], {
      name: "xterm-256color",
      cols,
      rows,
      env: { ...process.env, TERM: "xterm-256color" },
    })

    this.wire(sessionId, proc)
    return sessionId
  }

  writeToSession(sessionId: string, data: string): void {
    const proc = this.sessions.get(sessionId)
    if (!proc) throw new Error(`Session not found: ${sessionId}`)
    proc.write(data)
  }

  resizeSession(sessionId: string, cols: number, rows: number): void {
    const proc = this.sessions.get(sessionId)
    if (!proc) throw new Error(`Session not found: ${sessionId}`)
    proc.resize(cols, rows)
  }

  closeSession(sessionId: string): void {
    const proc = this.sessions.get(sessionId)
    if (!proc) throw new Error(`Session not found: ${sessionId}`)
    proc.kill()
    this.sessions.delete(sessionId)
  }

  listSessions(): string[] {
    return [...this.sessions.keys()]
  }

  destroyAll(): void {
    for (const [id, proc] of this.sessions) {
      proc.kill()
      this.sessions.delete(id)
    }
  }

  private wire(sessionId: string, proc: IPty): void {
    this.sessions.set(sessionId, proc)

    proc.onData((data) => {
      this.send("terminal:output", {
        sessionId,
        data: Array.from(new TextEncoder().encode(data)),
      })
    })

    proc.onExit(() => {
      this.sessions.delete(sessionId)
      this.send("terminal:exit", { sessionId })
    })
  }

  private send(channel: string, payload: unknown): void {
    const win = this.deps.getMainWindow()
    if (win && !win.isDestroyed()) {
      win.webContents.send(channel, payload)
    }
  }
}
