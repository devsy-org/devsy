import type { ChildProcess } from "node:child_process"
import { execFile as execFileCb, spawn } from "node:child_process"
import { createInterface } from "node:readline"
import { promisify } from "node:util"
import type { CLIError, CliLogLine } from "../shared/cli-error.js"
import { getAnalyticsDistinctId } from "./analytics.js"

const execFile = promisify(execFileCb)
const MAX_CONCURRENT = 50

export interface StreamLine {
  /** Raw line text from the CLI (already ANSI-stripped upstream is not assumed). */
  raw: string
  /** Parsed zap JSON log line, if the line was valid JSON. */
  parsed?: CliLogLine
  /** Convenience: the structured cliError extracted from a parsed line, if any. */
  cliError?: CLIError
  /** Log level from the parsed line, if available. */
  level?: "info" | "warn" | "error"
}

/**
 * Coerce an arbitrary zap level string into the narrow set the IPC payload uses.
 * Unknown levels map to "info".
 */
function normalizeLevel(level: unknown): "info" | "warn" | "error" | undefined {
  if (typeof level !== "string") return undefined
  const lower = level.toLowerCase()
  if (lower === "warn" || lower === "warning") return "warn"
  if (
    lower === "error" ||
    lower === "fatal" ||
    lower === "panic" ||
    lower === "dpanic"
  )
    return "error"
  return "info"
}

/**
 * Walk a multi-line stderr blob, parse each line as a zap JSON record, and
 * return the last `cliError` field encountered. Used by the non-streaming
 * `run`/`runRaw` paths where the entire stderr is delivered after exit.
 */
function extractCliErrorFromStderr(stderr: string): CLIError | undefined {
  let found: CLIError | undefined
  for (const line of stderr.split(/\r?\n/)) {
    const parsed = parseStderrLine(line)
    if (parsed?.cliError) found = parsed.cliError
  }
  return found
}

/**
 * Parse a single stderr line as a zap JSON record. Returns undefined when the
 * line is not valid JSON or not a plain object — callers should treat the line
 * as opaque text in that case.
 */
function parseStderrLine(line: string): CliLogLine | undefined {
  const trimmed = line.trim()
  if (!trimmed.startsWith("{")) return undefined
  try {
    const obj = JSON.parse(trimmed) as unknown
    if (obj && typeof obj === "object" && !Array.isArray(obj)) {
      return obj as CliLogLine
    }
  } catch {
    // not JSON — fall through
  }
  return undefined
}

/**
 * On macOS, GUI apps (including Electron) inherit a minimal PATH that excludes
 * /usr/local/bin and /opt/homebrew/bin. This means tools like docker, git, etc.
 * installed via Homebrew or Docker Desktop are invisible to child processes.
 * We augment PATH so all spawned CLI processes can find these tools.
 */
function buildEnv(): NodeJS.ProcessEnv {
  const baseEnv = {
    ...process.env,
    DEVSY_UI: "true",
    DEVSY_TELEMETRY_DISTINCT_ID: getAnalyticsDistinctId(),
  }

  if (process.platform !== "darwin") return baseEnv

  const currentPath = process.env.PATH ?? ""
  const extraDirs = [
    "/usr/local/bin",
    "/opt/homebrew/bin",
    "/opt/homebrew/sbin",
  ]
  const missing = extraDirs.filter((d) => !currentPath.split(":").includes(d))
  if (missing.length === 0) return baseEnv

  return {
    ...baseEnv,
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
      const fullArgs = [
        ...this.prefixArgs,
        ...args,
        "--result-format",
        "json",
        "--log-output",
        "json",
      ]
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
        [...this.prefixArgs, ...args, "--log-output", "json"],
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
    onLine: (
      line: string,
      stream: "stdout" | "stderr",
      meta?: StreamLine,
    ) => void,
    onExit: (code: number, cliError?: CLIError) => void,
  ): Promise<ChildProcess> {
    await this.acquire()
    const child = spawn(
      this.execPath,
      [...this.prefixArgs, ...args, "--log-output", "json"],
      { env: this.env },
    )

    this.activeChildren.add(child)

    let lastCliError: CLIError | undefined

    if (child.stdout) {
      const rl = createInterface({ input: child.stdout })
      rl.on("line", (line) => onLine(line, "stdout"))
    }

    if (child.stderr) {
      const rl = createInterface({ input: child.stderr })
      rl.on("line", (line) => {
        const parsed = parseStderrLine(line)
        if (parsed?.cliError) {
          lastCliError = parsed.cliError
        }
        const meta: StreamLine = {
          raw: line,
          parsed,
          cliError: parsed?.cliError,
          level: normalizeLevel(parsed?.level),
        }
        onLine(line, "stderr", meta)
      })
    }

    child.on("close", (code) => {
      this.activeChildren.delete(child)
      this.release()
      onExit(code ?? -1, lastCliError)
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

  private wrapError(error: unknown): Error & { cliError?: CLIError } {
    if (error instanceof Error && "stderr" in error) {
      const stderr = CliRunner.stripAnsi(
        String((error as { stderr: string }).stderr),
      )
      const cliError = extractCliErrorFromStderr(stderr)
      const message = cliError
        ? cliError.message
        : this.sanitizeMessage(stderr || error.message)
      const wrapped = new Error(message) as Error & { cliError?: CLIError }
      if (cliError) wrapped.cliError = cliError
      return wrapped
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
