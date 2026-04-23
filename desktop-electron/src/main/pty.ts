import type { IPty } from "node-pty"
import { platform } from "node:os"
import { dirname } from "node:path"
import type { BrowserWindow } from "electron"

// Lazy-load node-pty so the app can start even when the native module is unavailable
// (e.g. during e2e tests where native bindings aren't rebuilt for Electron's ABI).
let ptyModule: typeof import("node-pty") | null = null

function requirePty(): typeof import("node-pty") {
  if (!ptyModule) {
    try {
      // eslint-disable-next-line @typescript-eslint/no-require-imports
      ptyModule = require("node-pty") as typeof import("node-pty")
    } catch (err) {
      throw new Error(
        "Failed to load node-pty native module. Run: npx electron-rebuild -f -w node-pty",
        { cause: err },
      )
    }
  }
  return ptyModule
}

interface PtyDeps {
  binaryPath: string
  getMainWindow: () => BrowserWindow | null
}

export class PtyManager {
  private sessions = new Map<string, IPty>()
  private env: Record<string, string>

  constructor(private deps: PtyDeps) {
    // Build an augmented PATH so shells spawned from the Electron GUI can find
    // the bundled devsy binary and common macOS tool directories.
    const currentPath = process.env.PATH ?? ""
    const extraDirs = [dirname(deps.binaryPath)]
    if (platform() === "darwin") {
      extraDirs.push("/usr/local/bin", "/opt/homebrew/bin", "/opt/homebrew/sbin")
    }
    const missing = extraDirs.filter((d) => !currentPath.split(":").includes(d))
    const augmentedPath = missing.length > 0 ? `${missing.join(":")}:${currentPath}` : currentPath
    this.env = { ...process.env, TERM: "xterm-256color", PATH: augmentedPath } as Record<string, string>
  }

  createSession(cols: number, rows: number): string {
    const pty = requirePty()
    const shell =
      platform() === "win32"
        ? "powershell.exe"
        : process.env.SHELL || "/bin/sh"

    const sessionId = crypto.randomUUID()
    const proc = pty.spawn(shell, [], {
      name: "xterm-256color",
      cols,
      rows,
      env: this.env,
    })

    this.wire(sessionId, proc)
    return sessionId
  }

  createSshSession(workspaceId: string, cols: number, rows: number): string {
    const pty = requirePty()
    const sessionId = crypto.randomUUID()
    const proc = pty.spawn(this.deps.binaryPath, ["ssh", workspaceId], {
      name: "xterm-256color",
      cols,
      rows,
      env: this.env,
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

    proc.onExit(({ exitCode, signal }) => {
      this.sessions.delete(sessionId)
      this.send("terminal:exit", { sessionId, exitCode, signal })
    })
  }

  private send(channel: string, payload: unknown): void {
    const win = this.deps.getMainWindow()
    if (win && !win.isDestroyed()) {
      win.webContents.send(channel, payload)
    }
  }
}
