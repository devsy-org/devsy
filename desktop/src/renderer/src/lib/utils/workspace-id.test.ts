import { describe, expect, it } from "vitest"
import {
  deriveWorkspaceId,
  isValidWorkspaceId,
  uniqueWorkspaceId,
} from "./workspace-id.js"

describe("workspace IDs", () => {
  it.each(["backend", "backend-name-1", "a".repeat(48)])("accepts %s", (id) => {
    expect(isValidWorkspaceId(id)).toBe(true)
  })
  it.each(["", "Backend", "back_end", "back.end", "back end", "a".repeat(49)])(
    "rejects %s",
    (id) => {
      expect(isValidWorkspaceId(id)).toBe(false)
    },
  )
  it.each([
    "/var/home/dev/Projects/Backend",
    "https://example.com/Backend.git",
    "https://example.com/Backend.git@feature/foo",
    "git@host:org/Backend.git",
    "github.com/org/Backend@main@subpath:sub/path",
    "github.com/org/Backend@main:sub/path",
  ])("derives backend from %s without changing the source", (source) => {
    const original = source
    expect(deriveWorkspaceId(source)).toBe("backend")
    expect(source).toBe(original)
  })
  it("normalizes, truncates and falls back to valid IDs", () => {
    for (const source of ["a".repeat(80), "___", "App.Name With Spaces"])
      expect(isValidWorkspaceId(deriveWorkspaceId(source))).toBe(true)
  })
  it("preserves case insensitive collisions within the length limit", () => {
    const base = "a".repeat(48)
    const id = uniqueWorkspaceId(base, [base.toUpperCase()])
    expect(id).toBe(`${"a".repeat(46)}-1`)
    expect(isValidWorkspaceId(id)).toBe(true)
  })
})
