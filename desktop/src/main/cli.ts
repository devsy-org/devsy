import type { ChildProcess } from "node:child_process"
import { execFile as execFileCb, spawn } from "node:child_process"
import { createInterface } from "node:readline"
import { promisify } from "node:util"

const execFile = promisify(execFileCb)
const MAX_CONCURRENT = 50

/**
 * On macOS, GUI apps (including Electron) inherit a minimal PATH that excludes
 * /usr/local/bin and /opt/homebrew/bin. This means tools like docker, git, etc.
 * installed via Homebrew or Docker Desktop are invisible to child processes.
 * We augment PATH so all spawned CLI processes can find these tools.
 */
function buildEnv(): NodeJS.ProcessEnv {
  if (process.platform !== "darwin") return { ...process.env, DEVSY_UI: "true" }

  const currentPath = process.env.PATH ?? ""
  const extraDirs = [
    "/usr/local/bin",
    "/opt/homebrew/bin",
    "/opt/homebrew/sbin",
  ]
  const missing = extraDirs.filter((d) => !currentPath.split(":").includes(d))
  if (missing.length === 0) return { ...process.env, DEVSY_UI: "true" }

  return {
    ...process.env,
    DEVSY_UI: "true",
    PATH: `${missing.join(":")}:${currentPath}`,
  }
}

export class CliRunner {
  private execPath: string
  private prefixArgs: string[]
  private env: NodeJS.ProcessEnv
  private running = 0
  private queue: Array<() => void> = []

  constructor(private binaryPath: string) {
    if (/\.[cm]?js$/.test(binaryPath)) {
      this.execPath = "node"
      this.prefixArgs = [binaryPath]
    } else {
      this.execPath = binaryPath
      this.prefixArgs = []
    }
    this.env = buildEnv()
  }

  private acquire(): Promise<void> {
    if (this.running < MAX_CONCURRENT) {
      this.running++
      return Promise.resolve()
    }
    return new Promise((resolve) => {
      this.queue.push(() => {
        this.running++
        resolve()
      })
    })
  }

  private release(): void {
    this.running--
    const next = this.queue.shift()
    if (next) next()
  }

  async run<T>(args: string[]): Promise<T> {
    await this.acquire()
    try {
      const fullArgs = [...this.prefixArgs, ...args, "--output", "json"]
      const { stdout } = await execFile(this.execPath, fullArgs, {
        env: this.env,
      })
      return JSON.parse(stdout) as T
    } catch (error: unknown) {
      throw this.wrapError(error)
    } finally {
      this.release()
    }
  }

  async runRaw(args: string[]): Promise<string> {
    await this.acquire()
    try {
      const { stdout } = await execFile(
        this.execPath,
        [...this.prefixArgs, ...args],
        { env: this.env },
      )
      return stdout
    } catch (error: unknown) {
      throw this.wrapError(error)
    } finally {
      this.release()
    }
  }

  private activeChildren = new Set<ChildProcess>()

  async runStreaming(
    args: string[],
    onLine: (line: string, stream: "stdout" | "stderr") => void,
    onExit: (code: number) => void,
  ): Promise<ChildProcess> {
    await this.acquire()
    const child = spawn(this.execPath, [...this.prefixArgs, ...args], {
      env: this.env,
    })

    this.activeChildren.add(child)

    if (child.stdout) {
      const rl = createInterface({ input: child.stdout })
      rl.on("line", (line) => onLine(line, "stdout"))
    }

    if (child.stderr) {
      const rl = createInterface({ input: child.stderr })
      rl.on("line", (line) => onLine(line, "stderr"))
    }

    child.on("close", (code) => {
      this.activeChildren.delete(child)
      this.release()
      onExit(code ?? -1)
    })

    return child
  }

  killAll(): void {
    for (const child of this.activeChildren) {
      child.kill("SIGTERM")
    }
  }

  static stripAnsi(str: string): string {
    // biome-ignore lint/suspicious/noControlCharactersInRegex: ANSI escape sequences use control characters by definition
    return str.replace(/\x1b\[[0-9;]*[a-zA-Zm]/g, "")
  }

  static resolveBinaryPath(resourcesPath: string): string {
    const binaryName = process.platform === "win32" ? "devsy.exe" : "devsy"
    const { join } = require("node:path")
    return join(resourcesPath, "bin", binaryName)
  }

  private wrapError(error: unknown): Error {
    if (error instanceof Error && "stderr" in error) {
      const stderr = CliRunner.stripAnsi(
        String((error as { stderr: string }).stderr),
      )
      return new Error(this.sanitizeMessage(stderr || error.message))
    }
    return error instanceof Error
      ? new Error(this.sanitizeMessage(error.message))
      : new Error(String(error))
  }

  /** Strip the full binary path from error messages to avoid exposing system paths to the user. */
  private sanitizeMessage(msg: string): string {
    const binPath = this.binaryPath
    if (binPath && msg.includes(binPath)) {
      return msg.replaceAll(binPath, "devsy")
    }
    return msg
  }
}
