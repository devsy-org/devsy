import { describe, expect, it, vi } from "vitest"
import { buildUpdateMenuItems } from "../tray.js"

vi.mock("electron", () => ({}))
vi.mock("../updater.js", () => ({
  getLastStatus: () => ({ state: "idle" }),
  installUpdate: vi.fn(),
}))

describe("buildUpdateMenuItems", () => {
  it("returns nothing when no update is downloaded", () => {
    expect(buildUpdateMenuItems({ state: "idle", currentVersion: "1.0.0" }, () => {})).toEqual([])
    expect(buildUpdateMenuItems({ state: "checking", currentVersion: "1.0.0" }, () => {})).toEqual([])
    expect(
      buildUpdateMenuItems(
        { state: "available", currentVersion: "1.0.0", availableVersion: "1.1.0", version: "1.1.0" },
        () => {},
      ),
    ).toEqual([])
    expect(
      buildUpdateMenuItems(
        {
          state: "downloading",
          currentVersion: "1.0.0",
          availableVersion: "1.1.0",
          version: "1.1.0",
          progress: { percent: 50, bytesPerSecond: 1000, transferred: 50, total: 100 },
        },
        () => {},
      ),
    ).toEqual([])
    expect(buildUpdateMenuItems({ state: "not-available", currentVersion: "1.0.0" }, () => {})).toEqual([])
    expect(buildUpdateMenuItems({ state: "up-to-date", currentVersion: "1.0.0" }, () => {})).toEqual([])
    expect(
      buildUpdateMenuItems(
        { state: "error", currentVersion: "1.0.0", error: "x", code: "network" },
        () => {},
      ),
    ).toEqual([])
  })

  it("adds Install Update item + separator when downloaded", () => {
    const onInstall = vi.fn()
    const items = buildUpdateMenuItems(
      { state: "downloaded", currentVersion: "1.0.0", availableVersion: "9.9.9", version: "9.9.9" },
      onInstall,
    )
    expect(items).toHaveLength(2)
    expect(items[0]).toMatchObject({ label: "Install Update v9.9.9" })
    expect(items[1]).toEqual({ type: "separator" })

    const click = (items[0] as { click?: () => void }).click
    click?.()
    expect(onInstall).toHaveBeenCalledTimes(1)
  })

  it("handles missing version gracefully", () => {
    const items = buildUpdateMenuItems(
      { state: "downloaded", currentVersion: "1.0.0", availableVersion: "" },
      () => {},
    )
    expect(items[0]).toMatchObject({ label: "Install Update v" })
  })
})
