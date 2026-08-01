import { describe, expect, it } from "vitest"

import {
  isCommandSuccess,
  isRecoverableBuildFailure,
  parseRecoveryContainer,
} from "./log-parser.js"

describe("isCommandSuccess", () => {
  it("trusts an explicit success flag over the message", () => {
    // The flag comes from the real exit code, so it wins even when the text
    // would sniff the other way.
    expect(isCommandSuccess("Exit code: 0", false)).toBe(false)
    expect(isCommandSuccess("something went wrong", true)).toBe(true)
  })

  it("falls back to message sniffing when no flag is given", () => {
    expect(isCommandSuccess("Exit code: 0")).toBe(true)
    expect(isCommandSuccess("Exit code: 1")).toBe(false)
    expect(isCommandSuccess('{"outcome":"success"}')).toBe(true)
    expect(isCommandSuccess(null)).toBe(false)
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
