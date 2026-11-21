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

function install() {
  const target = new EventTarget()
  const report = vi.fn()
  const dispose = installRendererErrorReporting(
    target as unknown as Window,
    report,
  )
  return { target, report, dispose }
}

describe("renderer error reporting", () => {
  it("forwards the message unaltered so the main process can mask registered values first", () => {
    const { target, report, dispose } = install()
    const message = "Workspace load failed prefix-canary/suffix-canary?x={y}"
    const error = new TypeError(message)
    error.stack = "/Users/private/file.ts:1"
    emit(target, "error", "error", error)
    emit(target, "unhandledrejection", "reason", message)
    expect(report).toHaveBeenNthCalledWith(1, {
      kind: "error",
      message,
      name: "TypeError",
    })
    expect(report).toHaveBeenNthCalledWith(2, {
      kind: "unhandledrejection",
      message,
      name: "UnknownError",
    })
    expect(JSON.stringify(report.mock.calls)).not.toContain("/Users")
    dispose()
    emit(target, "error", "error", error)
    expect(report).toHaveBeenCalledTimes(2)
  })

  it.each([
    ["empty", ""],
    ["overlong", "x".repeat(4097)],
  ])("sends the generic message for an %s message", (_name, message) => {
    const { target, report } = install()
    emit(target, "error", "error", new Error(message))
    expect(report).toHaveBeenCalledWith({
      kind: "error",
      message: "Uncaught renderer error",
      name: "Error",
    })
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
