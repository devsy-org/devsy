import { describe, expect, it } from "vitest"

import {
  isRecoverableBuildFailure,
  parseRecoveryContainer,
} from "./log-parser.js"

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
