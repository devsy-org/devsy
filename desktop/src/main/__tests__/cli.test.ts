// @vitest-environment node
import { execFile, spawn } from "node:child_process"
import { EventEmitter } from "node:events"
import { Readable } from "node:stream"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { CliRunner } from "../cli.js"

vi.mock("../analytics.js", () => ({ getAnalyticsDistinctId: () => "test-id" }))

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

  it("scopes session credentials and strips inherited unlock environment from env commands", async () => {
    vi.stubEnv("DEVSY_SECRETS_PASSPHRASE", "inherited-passphrase")
    vi.stubEnv("DEVSY_SECRETS_PASSPHRASE_FILE", "/private/passphrase")
    try {
      cli = new CliRunner("/usr/local/bin/devsy")
      cli.setSessionPassphrase("session-passphrase")
      const mock = vi.mocked(execFile) as unknown as ReturnType<typeof vi.fn>
      mock.mockImplementation(
        (_cmd: string, _args: string[], _opts: unknown, callback: ExecCb) =>
          callback(null, { stdout: "[]", stderr: "" }),
      )
      await cli.run(["secret", "list"])
      await cli.run(["env", "list"], {
        env: { DEVSY_SECRETS_PASSPHRASE: "explicit-env" },
      })
      await cli.run(["workspace", "list"])
      await cli.runRaw([
        "--context",
        "secret",
        "env",
        "set",
        "workspace",
        "--value",
        "up",
      ])
      expect(mock.mock.calls[0][2].env.DEVSY_SECRETS_PASSPHRASE).toBe(
        "session-passphrase",
      )
      for (const call of mock.mock.calls.slice(1)) {
        expect(call[2].env.DEVSY_SECRETS_PASSPHRASE).toBeUndefined()
        expect(call[2].env.DEVSY_SECRETS_PASSPHRASE_FILE).toBeUndefined()
      }
      expect(process.env.DEVSY_SECRETS_PASSPHRASE).toBe("inherited-passphrase")
      cli.setSessionPassphrase(undefined)
      expect(cli.hasSessionPassphrase()).toBe(false)
    } finally {
      vi.unstubAllEnvs()
    }
  })

  it("prompts on a structured unlock requirement and retries exactly once without leaking credentials", async () => {
    const mock = vi.mocked(execFile) as unknown as ReturnType<typeof vi.fn>
    let attempts = 0
    mock.mockImplementation(
      (_cmd: string, _args: string[], _opts: unknown, callback: ExecCb) => {
        attempts++
        const code = attempts === 1 ? "unlock_required" : "unlock_failed"
        callback(
          Object.assign(new Error("failed"), {
            stderr: JSON.stringify({
              kind: "error",
              outcome: "error",
              code,
              message:
                attempts === 1
                  ? "Unlock required"
                  : "Unable to unlock session-private",
            }),
          }),
          { stdout: "", stderr: "" },
        )
      },
    )
    const unlock = vi.fn(async () => "session-private")
    cli.setUnlockHandler(unlock)
    await expect(cli.runRaw(["secret", "delete", "TOKEN"])).rejects.toThrow(
      "Unable to unlock ***",
    )
    expect(mock).toHaveBeenCalledTimes(2)
    expect(unlock).toHaveBeenCalledOnce()
    expect(cli.hasSessionPassphrase()).toBe(false)
    expect(mock.mock.calls[1][1]).not.toContain("session-private")
    expect(mock.mock.calls[1][2].env.DEVSY_SECRETS_PASSPHRASE).toBe(
      "session-private",
    )
  })

  it("passes the configured level without overriding explicit CLI levels", async () => {
    const mockExecFile = vi.mocked(execFile) as unknown as ReturnType<
      typeof vi.fn
    >
    mockExecFile.mockImplementation(
      (_cmd: string, _args: string[], _opts: unknown, callback: ExecCb) => {
        callback(null, { stdout: "{}", stderr: "" })
      },
    )
    cli.setDiagnosticLogLevel("debug")
    await cli.run(["workspace", "list"])
    expect(mockExecFile.mock.calls[0][1]).toContain("--log-level")
    expect(mockExecFile.mock.calls[0][1]).toContain("debug")
    await cli.run(["--log-level", "error", "workspace", "list"])
    const explicitArgs = mockExecFile.mock.calls[1][1] as string[]
    expect(explicitArgs.filter((arg) => arg === "--log-level")).toHaveLength(1)
    expect(explicitArgs).toContain("error")
  })

  it("owns protocol defaults and preserves explicit protocol arguments", async () => {
    const mockExecFile = vi.mocked(execFile) as unknown as ReturnType<
      typeof vi.fn
    >
    mockExecFile.mockImplementation(
      (_cmd: string, _args: string[], _opts: unknown, callback: ExecCb) => {
        callback(null, { stdout: "{}", stderr: "" })
      },
    )
    await cli.run(["workspace", "list", "--log-output", "text"])
    const args = mockExecFile.mock.calls[0][1] as string[]
    expect(args).toContain("--result-format")
    expect(args).toContain("json")
    expect(args).toContain("--log-output")
    expect(args).toContain("text")
    expect(args.filter((arg) => arg === "--result-format")).toHaveLength(1)
    expect(args.filter((arg) => arg === "--log-output")).toHaveLength(1)
  })

  it("rejects non-JSON result formats on run before executing", async () => {
    const mockExecFile = vi.mocked(execFile) as unknown as ReturnType<
      typeof vi.fn
    >
    await expect(
      cli.run(["workspace", "list", "--result-format=plain"]),
    ).rejects.toThrow("Use runRaw() for non-JSON output")
    expect(mockExecFile).not.toHaveBeenCalled()
  })

  it("recognizes the legacy log-format alias as an explicit protocol argument", async () => {
    const mockExecFile = vi.mocked(execFile) as unknown as ReturnType<
      typeof vi.fn
    >
    mockExecFile.mockImplementation(
      (_cmd: string, _args: string[], _opts: unknown, callback: ExecCb) => {
        callback(null, { stdout: "{}", stderr: "" })
      },
    )
    await cli.run(["workspace", "list", "--log-format=logfmt"])
    const args = mockExecFile.mock.calls[0][1] as string[]
    expect(args).toContain("--log-format=logfmt")
    expect(args.filter((arg) => arg === "--log-output")).toHaveLength(0)
  })

  it("defaults desktop-launched CLI diagnostics to info", async () => {
    const mockExecFile = vi.mocked(execFile) as unknown as ReturnType<
      typeof vi.fn
    >
    mockExecFile.mockImplementation(
      (_cmd: string, _args: string[], _opts: unknown, callback: ExecCb) => {
        callback(null, { stdout: "{}", stderr: "" })
      },
    )
    await cli.run(["workspace", "list"])
    expect(mockExecFile.mock.calls[0][1]).toContain("info")
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
          "--log-level",
          "info",
          "--result-format",
          "json",
          "--log-output",
          "json",
        ],
        expect.objectContaining({ env: expect.any(Object) }),
        expect.any(Function),
      )
    })

    it("parses the final result after structured status NDJSON", async () => {
      const mockExecFile = vi.mocked(execFile) as unknown as ReturnType<
        typeof vi.fn
      >
      mockExecFile.mockImplementation(
        (_cmd: string, _args: string[], _opts: unknown, callback: ExecCb) => {
          callback(null, {
            stdout:
              '{"kind":"status","schemaVersion":1,"phase":"building_image","state":"started"}\n' +
              '{"kind":"status","schemaVersion":1,"phase":"building_image","state":"succeeded","durationMs":12}\n' +
              '{"id":"ws-1"}\n',
            stderr: "",
          })
        },
      )

      await expect(
        cli.run<{ id: string }>(["workspace", "list"]),
      ).resolves.toEqual({ id: "ws-1" })
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
        message:
          "Rate limited by an upstream API. Wait and retry, or authenticate for a higher limit.",
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

    it("extracts a direct error envelope independently of diagnostic level", async () => {
      const mockExecFile = vi.mocked(execFile) as unknown as ReturnType<
        typeof vi.fn
      >
      const envelope = {
        kind: "error",
        outcome: "error",
        code: "BROKEN",
        message: "failure",
        hint: "retry",
      }
      mockExecFile.mockImplementation(
        (_cmd: string, _args: string[], _opts: unknown, callback: ExecCb) => {
          const error = new Error("Command failed") as Error & {
            code: number
            stderr: string
          }
          error.code = 1
          error.stderr = JSON.stringify(envelope)
          callback(error, { stdout: "", stderr: error.stderr })
        },
      )
      cli.setDiagnosticLogLevel("error")
      const rejection = await cli
        .run<never>(["workspace", "up", "."])
        .catch((e) => e as Error & { cliError?: unknown })
      expect(rejection.message).toBe("failure")
      expect(rejection.cliError).toEqual({
        code: "BROKEN",
        message: "failure",
        hint: "retry",
        context: undefined,
      })
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

      const promise = cli.runRawStdin(
        ["secret", "set", "FOO", "--stdin"],
        "bar",
      )
      await vi.waitFor(() => expect(mockSpawn).toHaveBeenCalled())

      // Regression guard: execFile's async { input } is a no-op, so the value must
      // reach stdin via spawn or the CLI blocks forever and the desktop hangs.
      expect(child.stdin.written).toBe("bar")

      child.stdout.emit("data", "ok\n")
      child.emit("close", 0)

      await expect(promise).resolves.toBe("ok\n")
      expect(mockSpawn).toHaveBeenCalledWith(
        "/usr/local/bin/devsy",
        [
          "secret",
          "set",
          "FOO",
          "--stdin",
          "--log-level",
          "info",
          "--log-output",
          "json",
        ],
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
          "--log-level",
          "info",
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
    it("redacts scoped credentials before delivering stdout, stderr, and structured errors", async () => {
      const child = fakeStreamingChild()
      vi.mocked(spawn).mockReturnValue(
        child as unknown as ReturnType<typeof spawn>,
      )
      cli.setSessionPassphrase("private-session-value")
      const onLine = vi.fn()
      const onExit = vi.fn()
      await cli.runStreaming(["workspace", "up", "."], onLine, onExit)
      child.stdout.push("echo private-session-value\n")
      child.stderr.push(
        `${JSON.stringify({ kind: "error", outcome: "error", code: "unlock_failed", message: "failed private-session-value" })}\n`,
      )
      await vi.waitFor(() => expect(onLine).toHaveBeenCalledTimes(2))
      child.emit("close", 1)
      expect(JSON.stringify(onLine.mock.calls)).not.toContain(
        "private-session-value",
      )
      expect(JSON.stringify(onExit.mock.calls)).not.toContain(
        "private-session-value",
      )
      expect(cli.hasSessionPassphrase()).toBe(false)
    })

    it("keeps streaming jobs active while unlocking and reports only the retry result", async () => {
      const first = fakeStreamingChild()
      const second = fakeStreamingChild()
      const mockSpawn = vi.mocked(spawn) as unknown as ReturnType<typeof vi.fn>
      mockSpawn.mockReturnValueOnce(first).mockReturnValueOnce(second)
      let unlock!: (value: string | undefined) => void
      cli.setUnlockHandler(
        () =>
          new Promise((resolve) => {
            unlock = resolve
          }),
      )
      const onLine = vi.fn()
      const onExit = vi.fn()
      await cli.runStreaming(
        ["workspace", "up", "."],
        onLine,
        onExit,
        "workspace-one",
      )
      first.stderr.push(
        `${JSON.stringify({ kind: "error", outcome: "error", code: "unlock_required", message: "Unlock required" })}\n`,
      )
      await new Promise((resolve) => setImmediate(resolve))
      first.emit("close", 1)
      await vi.waitFor(() => expect(unlock).toBeDefined())
      expect(onLine).not.toHaveBeenCalled()
      expect(onExit).not.toHaveBeenCalled()
      unlock("private-session")
      await vi.waitFor(() => expect(mockSpawn).toHaveBeenCalledTimes(2))
      second.stdout.push("workspace ready\n")
      await vi.waitFor(() => expect(onLine).toHaveBeenCalledOnce())
      second.emit("close", 0)
      expect(onExit).toHaveBeenCalledExactlyOnceWith(0, undefined)
      expect(mockSpawn.mock.calls[1][2].env.DEVSY_SECRETS_PASSPHRASE).toBe(
        "private-session",
      )
    })

    it("does not restart a workspace cancelled while the unlock dialog is pending", async () => {
      const first = fakeStreamingChild()
      const mockSpawn = vi.mocked(spawn) as unknown as ReturnType<typeof vi.fn>
      mockSpawn.mockReturnValue(first)
      let unlock!: (value: string | undefined) => void
      cli.setUnlockHandler(
        () =>
          new Promise((resolve) => {
            unlock = resolve
          }),
      )
      const onExit = vi.fn()
      await cli.runStreaming(
        ["workspace", "up", "."],
        () => undefined,
        onExit,
        "cancelled-workspace",
      )
      first.stderr.push(
        `${JSON.stringify({ kind: "error", outcome: "error", code: "unlock_required", message: "Unlock required" })}\n`,
      )
      await new Promise((resolve) => setImmediate(resolve))
      first.emit("close", 1)
      await vi.waitFor(() => expect(unlock).toBeDefined())
      await cli.cancelFor("cancelled-workspace")
      unlock("private-session")
      await vi.waitFor(() => expect(onExit).toHaveBeenCalledOnce())
      expect(mockSpawn).toHaveBeenCalledOnce()
    })

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

    it("passes a direct error envelope to the exit callback", async () => {
      const child = fakeStreamingChild()
      const mockSpawn = vi.mocked(spawn) as unknown as ReturnType<typeof vi.fn>
      mockSpawn.mockReturnValue(child)

      const onExit = vi.fn()
      const onLine = vi.fn()
      await cli.runStreaming(["workspace", "up", "."], onLine, onExit)
      child.stderr.push(
        `${JSON.stringify({
          kind: "error",
          outcome: "error",
          code: "BROKEN",
          message: "build failed",
        })}\n`,
      )
      child.stderr.push(null)
      await vi.waitFor(() => expect(onLine).toHaveBeenCalledTimes(1))
      child.emit("close", 1)

      await vi.waitFor(() => expect(onExit).toHaveBeenCalledTimes(1))
      expect(onExit.mock.calls[0]).toEqual([
        1,
        {
          code: "BROKEN",
          message: "build failed",
          hint: undefined,
          context: undefined,
        },
      ])
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
