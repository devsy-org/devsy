import { describe, expect, it } from "vitest"

import {
  isCommandSuccess,
  isRecoverableBuildFailure,
  parseLogLine,
  parseRecoveryContainer,
} from "./log-parser.js"

describe("parseLogLine", () => {
  it("does not interpret the removed legacy text format", () => {
    expect(parseLogLine("12:34:56 info old message source.go:42")).toEqual({
      time: "",
      level: "",
      message: "12:34:56 info old message source.go:42",
      source: "",
      origin: "",
    })
  })
})

describe("isCommandSuccess", () => {
  it("trusts an explicit success flag over the message", () => {
    expect(isCommandSuccess(false)).toBe(false)
    expect(isCommandSuccess(true)).toBe(true)
  })

  it("does not infer state from human-readable output", () => {
    expect(isCommandSuccess()).toBe(false)
  })
})

describe("isRecoverableBuildFailure", () => {
  it("matches the recoverable build-failure code", () => {
    expect(
      isRecoverableBuildFailure({ code: "BUILD_FAILED_RECOVERABLE" }),
    ).toBe(true)
  })

  it("does not match other error codes (e.g. compose/unknown)", () => {
    expect(isRecoverableBuildFailure({ code: "UNKNOWN" })).toBe(false)
  })

  it("returns false when there is no cliError", () => {
    expect(isRecoverableBuildFailure()).toBe(false)
    expect(isRecoverableBuildFailure(null)).toBe(false)
  })
})

describe("parseRecoveryContainer", () => {
  it("reads recovery=true from a success envelope", () => {
    expect(
      parseRecoveryContainer('{"outcome":"success","recovery":true}'),
    ).toBe(true)
  })

  it("returns false for a success envelope without recovery", () => {
    expect(parseRecoveryContainer('{"outcome":"success"}')).toBe(false)
  })

  it("returns null for non-success or non-envelope messages", () => {
    expect(parseRecoveryContainer("Exit code: 0")).toBe(null)
    expect(parseRecoveryContainer('{"outcome":"error"}')).toBe(null)
    expect(parseRecoveryContainer(null)).toBe(null)
  })
})
