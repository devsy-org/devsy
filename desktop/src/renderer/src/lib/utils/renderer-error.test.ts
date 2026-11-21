import { describe, expect, it, vi } from "vitest"
import { installRendererErrorReporting } from "./renderer-error.js"

function emit(
  target: EventTarget,
  type: string,
  field: string,
  value: unknown,
) {
  const event = new Event(type)
  Object.defineProperty(event, field, { value })
  target.dispatchEvent(event)
}

describe("renderer error reporting", () => {
  it("preserves useful messages while removing secrets, stacks, paths, and URLs", () => {
    const target = new EventTarget()
    const report = vi.fn()
    const dispose = installRendererErrorReporting(
      target as unknown as Window,
      report,
    )
    const error = new TypeError(
      "Workspace load failed token=secret https://example.test/?secret=123",
    )
    error.stack = "/Users/private/file.ts:1"
    emit(target, "error", "error", error)
    emit(
      target,
      "unhandledrejection",
      "reason",
      "Fetch failed /path?token=value",
    )
    const first = report.mock.calls[0][0]
    const second = report.mock.calls[1][0]
    expect(first.kind).toBe("error")
    expect(first.name).toBe("TypeError")
    expect(first.message).toContain("Workspace load failed")
    expect(second.message).toContain("Fetch failed")
    for (const secret of [
      "secret",
      "example.test",
      "123",
      "/Users",
      "/path",
      "token=value",
    ]) {
      expect(JSON.stringify(report.mock.calls)).not.toContain(secret)
    }
    dispose()
    emit(target, "error", "error", error)
    expect(report).toHaveBeenCalledTimes(2)
  })

  it.each([
    [
      String.raw`Request failed token="hidden\"escaped-canary" retry`,
      "Request failed [redacted] retry",
    ],
    [
      String.raw`Request failed token='hidden\'escaped-canary' retry`,
      "Request failed [redacted] retry",
    ],
    [
      `Request failed password="prefix truncated-canary ${"x".repeat(4096)}"`,
      "Request failed [redacted]",
    ],
    [
      String.raw`Request failed token=\"prefix encoded-canary\" retry`,
      "Request failed [redacted]",
    ],
  ])(
    "masks the whole quoted sensitive value before IPC",
    (message, expected) => {
      const target = new EventTarget()
      const report = vi.fn()
      installRendererErrorReporting(target as unknown as Window, report)
      emit(target, "error", "error", new Error(message))
      expect(report).toHaveBeenCalledWith({
        kind: "error",
        message: expected,
        name: "Error",
      })
    },
  )

  it.each([
    "access_token=access-canary",
    "client_secret is client-canary",
    "private key: private-canary",
    'accessToken=["array-canary", {"nested": ["nested-canary"]}]',
    'token= access_token="prefix quoted-tail-canary"',
    "password is client_secret is prose-tail-canary",
    `${"token= ".repeat(256)}"access_token": ["prefix", "group-tail-canary"]`,
    "Authorization: token auth-canary",
    'pRoXy-aUtHoRiZaTiOn: Extension "first alternate-canary"',
    "Authorization: 'first single-canary'",
    "Authorization: Basic Y2FuYXJ5IQ==",
    "request.headers.authorization=Extension qualified-canary",
    "password=alpha,beta;gamma",
  ])(
    "keeps compound credential values out of the reported payload",
    (field) => {
      const target = new EventTarget()
      const report = vi.fn()
      const dispose = installRendererErrorReporting(
        target as unknown as Window,
        report,
      )
      emit(
        target,
        "error",
        "error",
        new TypeError(`Request failed ${field} workspace=staging`),
      )
      expect(report).toHaveBeenCalledExactlyOnceWith({
        kind: "error",
        message: "Request failed [redacted] workspace=staging",
        name: "TypeError",
      })
      dispose()
    },
  )

  it.each([
    'client_secret={"nested": ["hidden"], "note": "remaining-canary"',
    'Authorization: Extension keyId="private identity",signature=signed-canary',
    'Authorization: Extension "complete" quoted-tail-canary',
    'Proxy-Authorization: Extension ["complete"] field=group-tail-canary workspace=staging',
    "Authorization: token auth-canary field=scalar-tail-canary workspace=staging",
    "Authorization: token auth-canary workspace=staging field=ambiguous-tail-canary",
  ])(
    "masks an incomplete or opaque credential field before forwarding",
    (field) => {
      const target = new EventTarget()
      const report = vi.fn()
      const dispose = installRendererErrorReporting(
        target as unknown as Window,
        report,
      )
      emit(target, "unhandledrejection", "reason", `Request failed ${field}`)
      expect(report).toHaveBeenCalledExactlyOnceWith({
        kind: "unhandledrejection",
        message: "Request failed [redacted]",
        name: "UnknownError",
      })
      dispose()
    },
  )

  it("does not inspect arbitrary thrown objects and tolerates a failed bridge", () => {
    const target = new EventTarget()
    const report = vi.fn(() => {
      throw new Error("bridge unavailable")
    })
    installRendererErrorReporting(target as unknown as Window, report)
    const thrown = {
      get message() {
        throw new Error("must not inspect")
      },
    }
    expect(() =>
      emit(target, "unhandledrejection", "reason", thrown),
    ).not.toThrow()
    expect(report).toHaveBeenCalledWith({
      kind: "unhandledrejection",
      message: "Unhandled renderer promise rejection",
      name: "UnknownError",
    })
  })
})
