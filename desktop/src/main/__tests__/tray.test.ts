import { describe, it, expect, vi } from "vitest"
import { buildUpdateMenuItems } from "../tray.js"

vi.mock("electron", () => ({}))
vi.mock("../updater.js", () => ({
  getLastStatus: () => ({ state: "idle" }),
  installUpdate: vi.fn(),
}))

describe("buildUpdateMenuItems", () => {
  it("returns nothing when no update is downloaded", () => {
    expect(buildUpdateMenuItems({ state: "idle" }, () => {})).toEqual([])
    expect(buildUpdateMenuItems({ state: "checking" }, () => {})).toEqual([])
    expect(buildUpdateMenuItems({ state: "available", version: "1" }, () => {})).toEqual([])
    expect(buildUpdateMenuItems({ state: "downloading", version: "1" }, () => {})).toEqual([])
    expect(buildUpdateMenuItems({ state: "not-available" }, () => {})).toEqual([])
    expect(buildUpdateMenuItems({ state: "error", error: "x" }, () => {})).toEqual([])
  })

  it("adds Install Update item + separator when downloaded", () => {
    const onInstall = vi.fn()
    const items = buildUpdateMenuItems({ state: "downloaded", version: "9.9.9" }, onInstall)
    expect(items).toHaveLength(2)
    expect(items[0]).toMatchObject({ label: "Install Update v9.9.9" })
    expect(items[1]).toEqual({ type: "separator" })

    const click = (items[0] as { click?: () => void }).click
    click?.()
    expect(onInstall).toHaveBeenCalledTimes(1)
  })

  it("handles missing version gracefully", () => {
    const items = buildUpdateMenuItems({ state: "downloaded" }, () => {})
    expect(items[0]).toMatchObject({ label: "Install Update v" })
  })
})
