import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { formatTimestamp, timeAgo, timeAgoMs } from "./time.js"

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

describe("relative timestamps with a frozen clock", () => {
  const now = 864000000

  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(now)
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it.each([
    [0, "Just now"],
    [59999, "Just now"],
    [60000, "1m ago"],
    [3599999, "59m ago"],
    [3600000, "1h ago"],
    [86399999, "23h ago"],
    [86400000, "1d ago"],
    [5 * 86400000 + 86399999, "5d ago"],
    [-1, "Just now"],
    [-86400000, "Just now"],
  ])(
    "formats %d milliseconds ago through both adapters",
    (elapsed, expected) => {
      const timestamp = now - elapsed
      const clock = vi.spyOn(Date, "now")

      expect(timeAgo(new Date(timestamp).toISOString())).toBe(expected)
      expect(clock).toHaveBeenCalledTimes(1)
      clock.mockClear()
      expect(timeAgoMs(timestamp)).toBe(expected)
      expect(clock).toHaveBeenCalledTimes(1)
    },
  )

  it.each([
    [0, "10d ago"],
    [-86400000, "11d ago"],
  ])(
    "formats epoch timestamp %d through both adapters",
    (timestamp, expected) => {
      expect(timeAgo(new Date(timestamp).toISOString())).toBe(expected)
      expect(timeAgoMs(timestamp)).toBe(expected)
    },
  )

  it.each([undefined, ""])(
    "avoids the clock for missing input %s",
    (timestamp) => {
      const clock = vi.spyOn(Date, "now").mockImplementation(() => {
        throw new Error("Clock must not be read")
      })

      expect(timeAgo(timestamp)).toBe("Unknown")
      expect(clock).not.toHaveBeenCalled()
    },
  )

  it.each(["not-a-date", "NaN", "Infinity", "-Infinity"])(
    "preserves invalid nonempty string %s",
    (timestamp) => {
      const clock = vi.spyOn(Date, "now")

      expect(timeAgo(timestamp)).toBe("NaNd ago")
      expect(clock).toHaveBeenCalledTimes(1)
    },
  )

  it.each([
    [NaN, "NaNd ago"],
    [Infinity, "Just now"],
    [-Infinity, "Infinityd ago"],
    [now - 60000 + 0.5, "Just now"],
    [now - 60000 - 0.5, "1m ago"],
    [-0.5, "10d ago"],
  ])("preserves numeric timestamp %s", (timestamp, expected) => {
    const clock = vi.spyOn(Date, "now")

    expect(timeAgoMs(timestamp)).toBe(expected)
    expect(clock).toHaveBeenCalledTimes(1)
  })

  it("reads the clock before constructing and reading the parsed date", () => {
    const events: string[] = []
    const OriginalDate = Date
    class ObservedDate extends OriginalDate {
      constructor(timestamp: string) {
        events.push("parse")
        super(timestamp)
      }

      getTime(): number {
        events.push("getTime")
        return super.getTime()
      }

      static now(): number {
        events.push("now")
        return now
      }
    }
    vi.stubGlobal("Date", ObservedDate)

    expect(timeAgo("1970-01-10T23:59:00Z")).toBe("1m ago")
    expect(events).toEqual(["now", "parse", "getTime"])
  })

  it("reads the clock before coercing the numeric timestamp", () => {
    const events: string[] = []
    vi.spyOn(Date, "now").mockImplementation(() => {
      events.push("now")
      return now
    })
    const timestamp = {
      valueOf() {
        events.push("timestamp")
        return now - 60000
      },
    }

    expect(timeAgoMs(timestamp as unknown as number)).toBe("1m ago")
    expect(events).toEqual(["now", "timestamp"])
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
