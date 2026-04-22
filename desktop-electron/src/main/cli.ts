import { execFile as execFileCb, spawn } from "node:child_process"
import { createInterface } from "node:readline"
import { promisify } from "node:util"
import type { ChildProcess } from "node:child_process"

const execFile = promisify(execFileCb)

export class CliRunner {
  constructor(private binaryPath: string) {}

  async run<T>(args: string[]): Promise<T> {
    const fullArgs = [...args, "--output", "json"]
    try {
      const { stdout } = await execFile(this.binaryPath, fullArgs)
      return JSON.parse(stdout) as T
    } catch (error: unknown) {
      throw this.wrapError(error)
    }
  }

  async runRaw(args: string[]): Promise<string> {
    try {
      const { stdout } = await execFile(this.binaryPath, args)
      return stdout
    } catch (error: unknown) {
      throw this.wrapError(error)
    }
  }

  runStreaming(
    args: string[],
    onLine: (line: string, stream: "stdout" | "stderr") => void,
    onExit: (code: number) => void,
  ): ChildProcess {
    const child = spawn(this.binaryPath, args)

    if (child.stdout) {
      const rl = createInterface({ input: child.stdout })
      rl.on("line", (line) => onLine(line, "stdout"))
    }

    if (child.stderr) {
      const rl = createInterface({ input: child.stderr })
      rl.on("line", (line) => onLine(line, "stderr"))
    }

    child.on("close", (code) => {
      onExit(code ?? -1)
    })

    return child
  }

  static stripAnsi(str: string): string {
    return str.replace(/\x1b\[[0-9;]*[a-zA-Zm]/g, "")
  }

  static resolveBinaryPath(resourcesPath: string): string {
    const binaryName = process.platform === "win32" ? "devpod.exe" : "devpod"
    const { join } = require("node:path")
    return join(resourcesPath, "bin", binaryName)
  }

  private wrapError(error: unknown): Error {
    if (error instanceof Error && "stderr" in error) {
      const stderr = CliRunner.stripAnsi(String((error as { stderr: string }).stderr))
      return new Error(stderr || error.message)
    }
    return error instanceof Error ? error : new Error(String(error))
  }
}
