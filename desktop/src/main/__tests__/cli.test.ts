// @vitest-environment node
import { execFile, spawn } from "node:child_process"
import { EventEmitter } from "node:events"
import { Readable } from "node:stream"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { CliRunner } from "../cli.js"

type ExecCb = (
  error: Error | null,
  result: { stdout: string; stderr: string },
) => void

function fakeChild() {
  const child = new EventEmitter() as EventEmitter & {
    stdout: EventEmitter
    stderr: EventEmitter
    stdin: EventEmitter & { end: (input: string) => void; written: string }
  }
  child.stdout = new EventEmitter()
  child.stderr = new EventEmitter()
  const stdin = new EventEmitter() as EventEmitter & {
    end: (input: string) => void
    written: string
  }
  stdin.written = ""
  stdin.end = (input: string) => {
    stdin.written = input
  }
  child.stdin = stdin
  return child
}

/** A child whose stdout/stderr are real streams, as readline requires. */
function fakeStreamingChild() {
  const child = new EventEmitter() as EventEmitter & {
    stdout: Readable
    stderr: Readable
  }
  child.stdout = new Readable({ read() {} })
  child.stderr = new Readable({ read() {} })
  return child
}

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
        (_cmd: string, _args: string[], _opts: unknown, callback: ExecCb) => {
          callback(null, { stdout: '[{"id":"ws-1"}]', stderr: "" })
        },
      )

      const result = await cli.run<{ id: string }[]>([
        "workspace",
        "list",
        "--skip-pro",
      ])
      expect(result).toEqual([{ id: "ws-1" }])
      expect(mockExecFile).toHaveBeenCalledWith(
        "/usr/local/bin/devsy",
        [
          "workspace",
          "list",
          "--skip-pro",
          "--result-format",
          "json",
          "--log-output",
          "json",
        ],
        expect.objectContaining({ env: expect.any(Object) }),
        expect.any(Function),
      )
    })

    it("throws on non-zero exit code with stripped ANSI stderr", async () => {
      const mockExecFile = vi.mocked(execFile) as unknown as ReturnType<
        typeof vi.fn
      >
      mockExecFile.mockImplementation(
        (_cmd: string, _args: string[], _opts: unknown, callback: ExecCb) => {
          const error = new Error("Command failed") as Error & {
            code: number
            stderr: string
          }
          error.code = 1
          error.stderr = "\x1b[31mError: workspace not found\x1b[0m"
          callback(error, { stdout: "", stderr: error.stderr })
        },
      )

      await expect(cli.run(["workspace", "list"])).rejects.toThrow(
        "workspace not found",
      )
    })

    it("extracts cliError from a zap JSON stderr line and attaches it to the thrown Error", async () => {
      const mockExecFile = vi.mocked(execFile) as unknown as ReturnType<
        typeof vi.fn
      >
      const cliErrorPayload = {
        code: "RATE_LIMITED",
        message: "Rate limited by an upstream API. Wait and retry, or authenticate for a higher limit.",
      }
      const stderrLine = JSON.stringify({
        level: "error",
        ts: "2026-05-25T06:02:47.423-0500",
        msg: cliErrorPayload.message,
        cliError: cliErrorPayload,
      })
      mockExecFile.mockImplementation(
        (_cmd: string, _args: string[], _opts: unknown, callback: ExecCb) => {
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
        .run<never>(["provider", "set", "aws"])
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
        (_cmd: string, _args: string[], _opts: unknown, callback: ExecCb) => {
          callback(null, { stdout: "v0.6.0-dev\n", stderr: "" })
        },
      )

      const result = await cli.runRaw(["--version"])
      expect(result).toBe("v0.6.0-dev\n")
    })
  })

  describe("runRawStdin", () => {
    it("writes the value to the child's stdin and resolves with stdout", async () => {
      const child = fakeChild()
      const mockSpawn = vi.mocked(spawn) as unknown as ReturnType<typeof vi.fn>
      mockSpawn.mockReturnValue(child)

      const promise = cli.runRawStdin(["secret", "set", "FOO", "--stdin"], "bar")
      await vi.waitFor(() => expect(mockSpawn).toHaveBeenCalled())

      // Regression guard: execFile's async { input } is a no-op, so the value must
      // reach stdin via spawn or the CLI blocks forever and the desktop hangs.
      expect(child.stdin.written).toBe("bar")

      child.stdout.emit("data", "ok\n")
      child.emit("close", 0)

      await expect(promise).resolves.toBe("ok\n")
      expect(mockSpawn).toHaveBeenCalledWith(
        "/usr/local/bin/devsy",
        ["secret", "set", "FOO", "--stdin", "--log-output", "json"],
        expect.objectContaining({ env: expect.any(Object) }),
      )
    })

    it("rejects with stderr on non-zero exit", async () => {
      const child = fakeChild()
      const mockSpawn = vi.mocked(spawn) as unknown as ReturnType<typeof vi.fn>
      mockSpawn.mockReturnValue(child)

      const promise = cli.runRawStdin(["secret", "set", "BAD", "--stdin"], "x")
      await vi.waitFor(() => expect(mockSpawn).toHaveBeenCalled())
      child.stderr.emit("data", "boom")
      child.emit("close", 1)

      await expect(promise).rejects.toThrow(/boom|exit code 1/)
    })
  })

  describe("constructor with .cjs binary", () => {
    it("runs .cjs files through node from PATH", async () => {
      const jsCli = new CliRunner("/tmp/mock.cjs")
      const mockExecFile = vi.mocked(execFile) as unknown as ReturnType<
        typeof vi.fn
      >
      mockExecFile.mockImplementation(
        (_cmd: string, _args: string[], _opts: unknown, callback: ExecCb) => {
          callback(null, { stdout: "[]", stderr: "" })
        },
      )

      await jsCli.run(["list"])
      expect(mockExecFile).toHaveBeenCalledWith(
        "node",
        [
          "/tmp/mock.cjs",
          "list",
          "--result-format",
          "json",
          "--log-output",
          "json",
        ],
        expect.objectContaining({ env: expect.any(Object) }),
        expect.any(Function),
      )
    })
  })

  describe("runStreaming", () => {
    it("reports an exit when the child fails to spawn", async () => {
      const child = fakeStreamingChild()
      const mockSpawn = vi.mocked(spawn) as unknown as ReturnType<typeof vi.fn>
      mockSpawn.mockReturnValue(child)

      const onExit = vi.fn()
      await cli.runStreaming(["provider", "init", "docker"], () => {}, onExit)
      child.emit("error", new Error("spawn ENOENT"))

      await vi.waitFor(() => expect(onExit).toHaveBeenCalledTimes(1))
      const [code, cliError] = onExit.mock.calls[0]
      expect(code).toBe(-1)
      expect(cliError?.message).toContain("spawn ENOENT")
    })

    it("reports the exit once when error and close both fire", async () => {
      const child = fakeStreamingChild()
      const mockSpawn = vi.mocked(spawn) as unknown as ReturnType<typeof vi.fn>
      mockSpawn.mockReturnValue(child)

      const onExit = vi.fn()
      await cli.runStreaming(["provider", "init", "docker"], () => {}, onExit)
      child.emit("error", new Error("boom"))
      child.emit("close", 1)

      await vi.waitFor(() => expect(onExit).toHaveBeenCalledTimes(1))
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
