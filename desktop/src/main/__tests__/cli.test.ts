// @vitest-environment node
import { execFile } from "node:child_process"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { CliRunner } from "../cli.js"

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
    cli = new CliRunner("/usr/local/bin/devsy")
  })

  describe("run", () => {
    it("parses JSON stdout and returns typed result", async () => {
      const mockExecFile = vi.mocked(execFile) as unknown as ReturnType<
        typeof vi.fn
      >
      mockExecFile.mockImplementation(
        (_cmd: string, _args: string[], _opts: unknown, callback: Function) => {
          callback(null, { stdout: '[{"id":"ws-1"}]', stderr: "" })
        },
      )

      const result = await cli.run<{ id: string }[]>(["list", "--skip-pro"])
      expect(result).toEqual([{ id: "ws-1" }])
      expect(mockExecFile).toHaveBeenCalledWith(
        "/usr/local/bin/devsy",
        ["list", "--skip-pro", "--result-format", "json", "--log-output", "json"],
        expect.objectContaining({ env: expect.any(Object) }),
        expect.any(Function),
      )
    })

    it("throws on non-zero exit code with stripped ANSI stderr", async () => {
      const mockExecFile = vi.mocked(execFile) as unknown as ReturnType<
        typeof vi.fn
      >
      mockExecFile.mockImplementation(
        (_cmd: string, _args: string[], _opts: unknown, callback: Function) => {
          const error = new Error("Command failed") as Error & {
            code: number
            stderr: string
          }
          error.code = 1
          error.stderr = "\x1b[31mError: workspace not found\x1b[0m"
          callback(error, { stdout: "", stderr: error.stderr })
        },
      )

      await expect(cli.run(["list"])).rejects.toThrow("workspace not found")
    })

    it("extracts cliError from a zap JSON stderr line and attaches it to the thrown Error", async () => {
      const mockExecFile = vi.mocked(execFile) as unknown as ReturnType<
        typeof vi.fn
      >
      const cliErrorPayload = {
        code: "AWS_PROFILE_MISSING",
        message: "AWS credentials are not configured.",
        hint: "Set AWS_PROFILE or create ~/.aws/credentials.",
        docUrl: "https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-files.html",
        provider: "aws",
        cause: "init: exit status 1: failed to get shared config profile, default",
      }
      const stderrLine = JSON.stringify({
        level: "error",
        ts: "2026-05-25T06:02:47.423-0500",
        msg: cliErrorPayload.message,
        cliError: cliErrorPayload,
      })
      mockExecFile.mockImplementation(
        (_cmd: string, _args: string[], _opts: unknown, callback: Function) => {
          const error = new Error("Command failed") as Error & {
            code: number
            stderr: string
          }
          error.code = 1
          error.stderr = `noise before\n${stderrLine}\n`
          callback(error, { stdout: "", stderr: error.stderr })
        },
      )

      const rejection = await cli
        .run(["provider", "set-options", "aws"])
        .catch((e) => e as Error & { cliError?: typeof cliErrorPayload })
      expect(rejection).toBeInstanceOf(Error)
      expect(rejection.cliError).toEqual(cliErrorPayload)
      expect(rejection.message).toBe(cliErrorPayload.message)
    })
  })

  describe("runRaw", () => {
    it("returns raw stdout string", async () => {
      const mockExecFile = vi.mocked(execFile) as unknown as ReturnType<
        typeof vi.fn
      >
      mockExecFile.mockImplementation(
        (_cmd: string, _args: string[], _opts: unknown, callback: Function) => {
          callback(null, { stdout: "v0.6.0-dev\n", stderr: "" })
        },
      )

      const result = await cli.runRaw(["version"])
      expect(result).toBe("v0.6.0-dev\n")
    })
  })

  describe("constructor with .cjs binary", () => {
    it("runs .cjs files through node from PATH", async () => {
      const jsCli = new CliRunner("/tmp/mock.cjs")
      const mockExecFile = vi.mocked(execFile) as unknown as ReturnType<
        typeof vi.fn
      >
      mockExecFile.mockImplementation(
        (_cmd: string, _args: string[], _opts: unknown, callback: Function) => {
          callback(null, { stdout: "[]", stderr: "" })
        },
      )

      await jsCli.run(["list"])
      expect(mockExecFile).toHaveBeenCalledWith(
        "node",
        ["/tmp/mock.cjs", "list", "--result-format", "json", "--log-output", "json"],
        expect.objectContaining({ env: expect.any(Object) }),
        expect.any(Function),
      )
    })
  })

  describe("stripAnsi", () => {
    it("removes ANSI escape sequences", () => {
      const result = CliRunner.stripAnsi(
        "\x1b[31mred\x1b[0m normal \x1b[1mbold\x1b[m",
      )
      expect(result).toBe("red normal bold")
    })
  })
})
