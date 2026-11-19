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
