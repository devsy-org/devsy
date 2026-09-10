import { describe, expect, it } from "vitest"
import {
  isActiveWorkspaceStatus,
  normalizeWorkspaceStatus,
} from "../workspace-status.js"

describe("workspace status", () => {
  it.each([
    ['{"state":"running"}', "running"],
    ['{"state":"busy"}', "busy"],
    [" stopped ", "stopped"],
    ["not-json", "not-json"],
  ])("normalizes %j", (raw, expected) => {
    expect(normalizeWorkspaceStatus(raw)).toBe(expected)
  })

  it("ignores empty or JSON without a state", () => {
    expect(normalizeWorkspaceStatus("  ")).toBeUndefined()
    expect(normalizeWorkspaceStatus("{}")).toBeUndefined()
  })

  it.each(["running", "RUNNING", "busy", "Busy"])(
    "classifies %s as active",
    (status) => expect(isActiveWorkspaceStatus(status)).toBe(true),
  )

  it.each([undefined, "stopped", "notfound", "unknown"])(
    "classifies %s as inactive",
    (status) => expect(isActiveWorkspaceStatus(status)).toBe(false),
  )
})
