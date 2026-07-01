import { describe, it, expect } from "vitest"
import { fmtMBps, statusHeadline } from "./status-copy.js"

describe("fmtMBps", () => {
  it("formats bytes/sec as MB/s", () => {
    expect(fmtMBps(1_500_000)).toBe("1.50 MB/s")
  })

  it("returns empty for zero/undefined", () => {
    expect(fmtMBps(0)).toBe("")
    expect(fmtMBps(undefined)).toBe("")
  })
})

describe("statusHeadline", () => {
  it("announces up-to-date with version", () => {
    expect(statusHeadline({ state: "not-available" }, "1.2.3")).toBe(
      "Devsy is up to date · v1.2.3",
    )
  })

  it("announces an available version", () => {
    expect(statusHeadline({ state: "available", version: "2.0.0" }, "1.2.3")).toBe(
      "Version 2.0.0 is available",
    )
  })

  it("announces download progress", () => {
    expect(
      statusHeadline(
        {
          state: "downloading",
          version: "2.0.0",
          progress: { percent: 42, bytesPerSecond: 0, transferred: 0, total: 0 },
        },
        "1.2.3",
      ),
    ).toBe("Downloading v2.0.0 · 42%")
  })

  it("announces ready-to-install", () => {
    expect(statusHeadline({ state: "downloaded", version: "2.0.0" }, "1.2.3")).toBe(
      "Version 2.0.0 is ready to install",
    )
  })

  it("distinguishes dev-mode and channel-missing", () => {
    expect(statusHeadline({ state: "not-available", code: "dev-mode" }, null)).toBe(
      "Updates run in packaged builds",
    )
    expect(
      statusHeadline({ state: "not-available", code: "channel-missing" }, "1.2.3"),
    ).toBe("No releases on this channel yet")
  })
})
