// @vitest-environment node
import { describe, it, expect, vi, beforeEach } from "vitest"
import { CliRunner } from "../cli.js"
import { execFile, spawn } from "node:child_process"
import type { ChildProcess } from "node:child_process"
import { EventEmitter, Readable } from "node:stream"

vi.mock("node:child_process", async (importOriginal) => {
  const actual = await importOriginal<typeof import("node:child_process")>()
  return {
    ...actual,
    execFile: vi.fn(),
    spawn: vi.fn(),
  }
})

describe("CliRunner", () => {
  let cli: CliRunner

  beforeEach(() => {
    vi.clearAllMocks()
    cli = new CliRunner("/usr/local/bin/devpod")
  })

  describe("run", () => {
    it("parses JSON stdout and returns typed result", async () => {
      const mockExecFile = vi.mocked(execFile) as unknown as ReturnType<typeof vi.fn>
      mockExecFile.mockImplementation((_cmd: string, _args: string[], callback: Function) => {
        callback(null, { stdout: '[{"id":"ws-1"}]', stderr: "" })
      })

      const result = await cli.run<{ id: string }[]>(["list", "--skip-pro"])
      expect(result).toEqual([{ id: "ws-1" }])
      expect(mockExecFile).toHaveBeenCalledWith(
        "/usr/local/bin/devpod",
        ["list", "--skip-pro", "--output", "json"],
        expect.any(Function),
      )
    })

    it("throws on non-zero exit code with stripped ANSI stderr", async () => {
      const mockExecFile = vi.mocked(execFile) as unknown as ReturnType<typeof vi.fn>
      mockExecFile.mockImplementation((_cmd: string, _args: string[], callback: Function) => {
        const error = new Error("Command failed") as Error & { code: number; stderr: string }
        error.code = 1
        error.stderr = "\x1b[31mError: workspace not found\x1b[0m"
        callback(error, { stdout: "", stderr: error.stderr })
      })

      await expect(cli.run(["list"])).rejects.toThrow("workspace not found")
    })
  })

  describe("runRaw", () => {
    it("returns raw stdout string", async () => {
      const mockExecFile = vi.mocked(execFile) as unknown as ReturnType<typeof vi.fn>
      mockExecFile.mockImplementation((_cmd: string, _args: string[], callback: Function) => {
        callback(null, { stdout: "v0.6.0-dev\n", stderr: "" })
      })

      const result = await cli.runRaw(["version"])
      expect(result).toBe("v0.6.0-dev\n")
    })
  })

  describe("stripAnsi", () => {
    it("removes ANSI escape sequences", () => {
      const result = CliRunner.stripAnsi("\x1b[31mred\x1b[0m normal \x1b[1mbold\x1b[m")
      expect(result).toBe("red normal bold")
    })
  })
})
