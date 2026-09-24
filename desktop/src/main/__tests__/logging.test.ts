// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest"
import { getMainLogLevel, mainLog, setMainLogLevel } from "../logging.js"

describe("main logger", () => {
  afterEach(() => setMainLogLevel("info"))

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
    expect(debug).toHaveBeenCalledWith("visible")
    debug.mockRestore()
  })
})
