import { afterEach, describe, expect, it, vi } from "vitest"
import { formatTimestamp, timeAgo } from "./time.js"

describe("timeAgo", () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it("returns 'Unknown' for undefined input", () => {
    expect(timeAgo(undefined)).toBe("Unknown")
  })

  it("returns 'Unknown' for empty string", () => {
    expect(timeAgo("")).toBe("Unknown")
  })

  it("returns 'Just now' for timestamps less than a minute ago", () => {
    const now = new Date().toISOString()
    expect(timeAgo(now)).toBe("Just now")
  })

  it("returns minutes ago for recent timestamps", () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date("2026-01-15T12:30:00Z"))
    expect(timeAgo("2026-01-15T12:25:00Z")).toBe("5m ago")
  })

  it("returns hours ago for timestamps within a day", () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date("2026-01-15T15:00:00Z"))
    expect(timeAgo("2026-01-15T12:00:00Z")).toBe("3h ago")
  })

  it("returns days ago for older timestamps", () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date("2026-01-20T12:00:00Z"))
    expect(timeAgo("2026-01-15T12:00:00Z")).toBe("5d ago")
  })

  it("handles boundary: exactly 60 minutes shows 1h", () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date("2026-01-15T13:00:00Z"))
    expect(timeAgo("2026-01-15T12:00:00Z")).toBe("1h ago")
  })

  it("handles boundary: exactly 24 hours shows 1d", () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date("2026-01-16T12:00:00Z"))
    expect(timeAgo("2026-01-15T12:00:00Z")).toBe("1d ago")
  })

  it("handles boundary: 59 minutes stays in minutes", () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date("2026-01-15T12:59:00Z"))
    expect(timeAgo("2026-01-15T12:00:00Z")).toBe("59m ago")
  })

  it("handles boundary: 23 hours stays in hours", () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date("2026-01-15T11:00:00Z"))
    expect(timeAgo("2026-01-14T12:00:00Z")).toBe("23h ago")
  })
})

describe("formatTimestamp", () => {
  it("formats a valid ISO timestamp", () => {
    const result = formatTimestamp("2026-01-15T12:00:00Z")
    // Output is locale-dependent, just verify it's not the raw string
    expect(result).not.toBe("2026-01-15T12:00:00Z")
    expect(result.length).toBeGreaterThan(0)
  })

  it("returns the input for an invalid date string", () => {
    expect(formatTimestamp("not-a-date")).toBe("Invalid Date")
  })

  it("handles empty string", () => {
    const result = formatTimestamp("")
    expect(typeof result).toBe("string")
  })
})
