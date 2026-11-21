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

  it("masks registered numeric values before writing either sink", () => {
    const dir = mkdtempSync(join(tmpdir(), "devsy-numeric-log-"))
    const info = vi.spyOn(console, "info").mockImplementation(() => {})
    process.env.TEST_LOG_SECRET = "731946"
    try {
      initializeMainLog(dir)
      mainLog.info(731946, { nested: [731946n, 819, 132n] })
      const file = readFileSync(join(dir, "main.log"), "utf8").trim()
      expect(info.mock.calls[0][0]).toBe(file)
      expect(JSON.parse(file)).toMatchObject({
        value: "***",
        args: [{ nested: ["***", 819, "132"] }],
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

  it("masks labeled, URL, and registered credentials when their ranges overlap", () => {
    const dir = mkdtempSync(join(tmpdir(), "devsy-overlapping-log-"))
    const info = vi.spyOn(console, "info").mockImplementation(() => {})
    vi.stubEnv("TEST_LOG_SECRET", "token")
    vi.stubEnv("TEST_LOG_URL_SECRET", "https://user")
    vi.stubEnv(
      "TEST_LOG_OVERLAP_SECRET",
      "registered-head password=overlap-canary",
    )
    try {
      initializeMainLog(dir)
      mainLog.info(
        "visible token=labeled-canary https://user:url-canary@example.test/root registered-head password=overlap-canary visible-tail",
      )
      const file = readFileSync(join(dir, "main.log"), "utf8").trim()
      expect(info.mock.calls[0][0]).toBe(file)
      expect(JSON.parse(file).msg).toBe(
        "visible ***=*** ***@example.test/root *** visible-tail",
      )
    } finally {
      vi.unstubAllEnvs()
      rmSync(dir, { recursive: true, force: true })
    }
  })

  it.each([
    [
      "https://alice:pw@private-canary@example.test/root",
      "https://***@example.test/root",
    ],
    [
      "https://alice:pw@private-canary@example.test",
      "https://***@example.test",
    ],
    [
      "https://alice:pw@private-canary@example.test?q=1",
      "https://***@example.test?q=1",
    ],
    ["https://alice@example.test/root", "https://***@example.test/root"],
    ["https://example.test/root@public", "https://example.test/root@public"],
  ])(
    "masks URL credentials through the last @ in the authority",
    (input, expected) => {
      const dir = mkdtempSync(join(tmpdir(), "devsy-authority-log-"))
      const info = vi.spyOn(console, "info").mockImplementation(() => {})
      try {
        initializeMainLog(dir)
        mainLog.info(`visit ${input} now`)
        const file = readFileSync(join(dir, "main.log"), "utf8").trim()
        expect(info.mock.calls[0][0]).toBe(file)
        expect(JSON.parse(file).msg).toBe(`visit ${expected} now`)
      } finally {
        rmSync(dir, { recursive: true, force: true })
      }
    },
  )

  it("keeps Error diagnostics without reading own or inherited accessors", () => {
    const dir = mkdtempSync(join(tmpdir(), "devsy-error-accessors-"))
    const info = vi.spyOn(console, "info").mockImplementation(() => {})
    const readName = vi.fn(() => {
      throw new Error("name accessor ran")
    })
    const readMessage = vi.fn(() => "message access should be skipped")
    const own = new Error("ordinary")
    Object.defineProperties(own, {
      name: { get: readName },
      message: { get: readMessage },
    })
    class InheritedError extends Error {}
    Object.defineProperty(InheritedError.prototype, "message", {
      get: readMessage,
    })
    try {
      initializeMainLog(dir)
      mainLog.info(
        "operation failed",
        own,
        new InheritedError(),
        new TypeError("details token=type-error-canary"),
      )
      const file = readFileSync(join(dir, "main.log"), "utf8").trim()
      expect(info.mock.calls[0][0]).toBe(file)
      expect(readName).not.toHaveBeenCalled()
      expect(readMessage).not.toHaveBeenCalled()
      expect(JSON.parse(file)).toMatchObject({
        msg: "operation failed",
        args: [
          { name: "Error", message: "" },
          { name: "Error", message: "" },
          { name: "TypeError", message: "details token=***" },
        ],
      })
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })

  it("masks chained assignments in both sinks and preserves the next field", () => {
    const dir = mkdtempSync(join(tmpdir(), "devsy-chained-log-"))
    const info = vi.spyOn(console, "info").mockImplementation(() => {})
    try {
      initializeMainLog(dir)
      mainLog.info(
        'Request failed token= access_token="prefix quoted-tail-canary" workspace=staging',
      )
      const file = readFileSync(join(dir, "main.log"), "utf8").trim()
      expect(info.mock.calls[0][0]).toBe(file)
      expect(JSON.parse(file).msg).toBe(
        "Request failed token= *** workspace=staging",
      )
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })

  it("masks scheme credentials in both sinks and preserves following fields for complete values", () => {
    const dir = mkdtempSync(join(tmpdir(), "devsy-authorization-log-"))
    const info = vi.spyOn(console, "info").mockImplementation(() => {})
    const fixtures = [
      [
        "request.headers.authorization=Extension qualified-canary workspace=staging",
        "request.headers.authorization=*** workspace=staging",
      ],
      [
        "password=alpha,beta;gamma workspace=staging",
        "password=*** workspace=staging",
      ],
      [
        "Authorization: token auth-canary workspace=staging",
        "Authorization: *** workspace=staging",
      ],
      [
        'Proxy-Authorization: Extension "first alternate-canary" workspace=staging',
        "Proxy-Authorization: *** workspace=staging",
      ],
      [
        "Authorization: 'first single-canary' workspace=staging",
        "Authorization: *** workspace=staging",
      ],
      [
        "Authorization: Basic Y2FuYXJ5IQ== workspace=staging",
        "Authorization: *** workspace=staging",
      ],
      [
        'Authorization: Extension keyId="private identity",signature=signed-canary',
        "Authorization: ***",
      ],
      [
        'Proxy-Authorization: Extension ["complete"] field=group-tail-canary workspace=staging',
        "Proxy-Authorization: ***",
      ],
      [
        "Authorization: token auth-canary workspace=staging field=ambiguous-tail-canary",
        "Authorization: ***",
      ],
      [
        "Authorization: Basic Y2FuYXJ5IQ==\r\n workspace=folded-canary",
        "Authorization: ***",
      ],
      [
        'Proxy-Authorization: Extension "first"\n\tworkspace=folded-canary',
        "Proxy-Authorization: ***",
      ],
      [
        "Authorization:\r\n Bearer auth-canary workspace=folded-canary",
        "Authorization:\r\n ***",
      ],
      [
        "Authorization: Bearer\r\n auth-canary workspace=folded-canary",
        "Authorization: ***",
      ],
      [
        'Proxy-Authorization: Extension "first\u2028second" workspace=folded-canary',
        "Proxy-Authorization: ***",
      ],
    ]
    try {
      initializeMainLog(dir)
      for (const [input] of fixtures) mainLog.info(`Request failed ${input}`)
      const file = readFileSync(join(dir, "main.log"), "utf8")
        .trim()
        .split("\n")
      expect(info.mock.calls.map(([record]) => record)).toEqual(file)
      expect(file.map((record) => JSON.parse(record).msg)).toEqual(
        fixtures.map(([, output]) => `Request failed ${output}`),
      )
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })
})
