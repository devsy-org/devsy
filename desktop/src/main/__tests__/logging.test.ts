import {
  mkdtempSync,
  readdirSync,
  readFileSync,
  rmSync,
  statSync,
} from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { afterEach, describe, expect, it, vi } from "vitest"
import {
  getMainLogLevel,
  initializeMainLog,
  mainLog,
  setMainLogLevel,
} from "../logging.js"

describe("main logger", () => {
  afterEach(() => {
    delete process.env.DEVSY_LOG_LEVEL
    delete process.env.TEST_LOG_SECRET
    setMainLogLevel("info")
    vi.restoreAllMocks()
  })

  it("filters diagnostics without replacing global console methods", () => {
    const originalInfo = console.info
    setMainLogLevel("error")
    expect(console.info).toBe(originalInfo)
    const info = vi.spyOn(console, "info").mockImplementation(() => {})
    mainLog.info("hidden")
    mainLog.error("visible")
    expect(info).not.toHaveBeenCalled()
    expect(getMainLogLevel()).toBe("error")
    info.mockRestore()
    expect(console.info).toBe(originalInfo)
  })

  it("emits trace diagnostics only at the trace level", () => {
    const debug = vi.spyOn(console, "debug").mockImplementation(() => {})
    setMainLogLevel("debug")
    mainLog.trace("hidden")
    setMainLogLevel("trace")
    mainLog.trace("visible")
    expect(debug).toHaveBeenCalledTimes(1)
    expect(JSON.parse(debug.mock.calls[0][0] as string)).toMatchObject({
      level: "trace",
      msg: "visible",
    })
    debug.mockRestore()
  })

  it("enforces environment precedence and rejects invalid configuration", () => {
    process.env.DEVSY_LOG_LEVEL = "debug"
    setMainLogLevel("error")
    expect(getMainLogLevel()).toBe("debug")
    process.env.DEVSY_LOG_LEVEL = "invalid"
    expect(() => setMainLogLevel("info")).toThrow("Invalid DEVSY_LOG_LEVEL")
  })

  it("redacts sensitive labels and quoted values in messages and errors in both sinks", () => {
    const dir = mkdtempSync(join(tmpdir(), "devsy-redaction-"))
    const error = vi.spyOn(console, "error").mockImplementation(() => {})
    try {
      initializeMainLog(dir)
      mainLog.error(
        `message-canary authorization=auth-value private_key=private-value access_key=access-value token="alpha beta" api-key='gamma delta' telemetry distinct id=identity-value`,
        new Error(
          `error-canary authorization: Bearer bearer-value private_key="quoted secret" access_key='two words' telemetry distinct id="error identity"`,
        ),
      )
      const file = readFileSync(join(dir, "main.log"), "utf8")
      const consoleRecord = error.mock.calls[0][0] as string
      for (const record of [file, consoleRecord]) {
        expect(record).toContain("message-canary")
        expect(record).toContain("error-canary")
        for (const secret of [
          "auth-value",
          "private-value",
          "access-value",
          "alpha beta",
          "gamma delta",
          "bearer-value",
          "quoted secret",
          "two words",
          "identity-value",
          "error identity",
        ]) {
          expect(record).not.toContain(secret)
        }
      }
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })

  it("preserves long nonsecret text while redacting a trailing sensitive label", () => {
    const dir = mkdtempSync(join(tmpdir(), "devsy-long-log-"))
    const info = vi.spyOn(console, "info").mockImplementation(() => {})
    try {
      initializeMainLog(dir)
      const canary = "x".repeat(20_000)
      mainLog.info(`${canary} access_key="long secret" tail-canary`)
      const file = readFileSync(join(dir, "main.log"), "utf8")
      const consoleRecord = info.mock.calls[0][0] as string
      for (const record of [file, consoleRecord]) {
        expect(record).toContain(canary)
        expect(record).toContain("tail-canary")
        expect(record).not.toContain("long secret")
      }
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })

  it("redacts passphrases and keeps caller serialization hooks out of both sinks", () => {
    const dir = mkdtempSync(join(tmpdir(), "devsy-serialization-log-"))
    const info = vi.spyOn(console, "info").mockImplementation(() => {})
    const serialize = vi.fn(() => ({ token: "serialization-canary" }))
    const array = [{ token: "array-value", count: 1 }]
    const map = vi.fn(() => [{ token: "array-map-canary" }])
    Object.defineProperty(array, "map", { value: map })
    const readIndex = vi.fn(() => ({ token: "indexed-getter-canary" }))
    Object.defineProperty(array, 1, { get: readIndex })
    const accessor = { operation: "report" }
    const readSerializer = vi.fn(() => serialize)
    Object.defineProperty(accessor, "toJSON", {
      get: readSerializer,
      enumerable: true,
    })
    try {
      initializeMainLog(dir)
      mainLog.info(
        "Unlock failed passphrase=message-canary",
        {
          toJSON: serialize,
          passphrase: "attribute-canary",
          operation: "unlock",
        },
        array,
        accessor,
      )
      const file = readFileSync(join(dir, "main.log"), "utf8").trim()
      expect(info.mock.calls[0][0]).toBe(file)
      expect(serialize).not.toHaveBeenCalled()
      expect(map).not.toHaveBeenCalled()
      expect(readIndex).not.toHaveBeenCalled()
      expect(readSerializer).not.toHaveBeenCalled()
      expect(JSON.parse(file)).toMatchObject({
        msg: "Unlock failed passphrase=***",
        args: [
          { operation: "unlock", passphrase: "***" },
          [{ token: "***", count: 1 }, null],
          { operation: "report" },
        ],
      })
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })

  it("redacts structured values before both sinks and bounds rotation", () => {
    const dir = mkdtempSync(join(tmpdir(), "devsy-main-log-"))
    const info = vi.spyOn(console, "info").mockImplementation(() => {})
    process.env.TEST_LOG_SECRET = "sensitive-example"
    try {
      initializeMainLog(dir)
      mainLog.child("test").info("safe-canary sensitive-example", {
        token: "private",
        nested: ["sensitive-example"],
      })
      const first = readFileSync(join(dir, "main.log"), "utf8")
      expect(first).toContain("safe-canary")
      expect(first).not.toContain("sensitive-example")
      expect(first).not.toContain("private")
      expect(info.mock.calls[0][0]).toBe(first.trim())
      for (let i = 0; i < 340; i++) mainLog.info("x".repeat(20_000))
      const files = readdirSync(dir)
      expect(files).toContain("main.log.1")
      expect(files.length).toBeLessThanOrEqual(4)
      for (const file of files)
        expect(statSync(join(dir, file)).size).toBeLessThanOrEqual(
          5 * 1024 * 1024,
        )
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })
})
