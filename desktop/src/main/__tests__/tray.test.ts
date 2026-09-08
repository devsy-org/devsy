import { describe, expect, it, vi } from "vitest"
import { buildTrayMenuTemplate, buildUpdateMenuItems } from "../tray.js"

vi.mock("electron", () => ({}))
vi.mock("../updater.js", () => ({
  getLastStatus: () => ({ state: "idle" }),
  installUpdate: vi.fn(),
  onUpdateStatusChanged: vi.fn(() => () => {}),
}))

describe("buildUpdateMenuItems", () => {
  it("returns nothing when no update is downloaded", () => {
    expect(buildUpdateMenuItems({ state: "idle", currentVersion: "1.0.0" }, () => {})).toEqual([])
    expect(buildUpdateMenuItems({ state: "checking", currentVersion: "1.0.0" }, () => {})).toEqual([])
    expect(
      buildUpdateMenuItems(
        { state: "available", currentVersion: "1.0.0", availableVersion: "1.1.0" },
        () => {},
      ),
    ).toEqual([])
    expect(
      buildUpdateMenuItems(
        {
          state: "downloading",
          currentVersion: "1.0.0",
          availableVersion: "1.1.0",
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
      { state: "downloaded", currentVersion: "1.0.0", availableVersion: "9.9.9" },
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

  it("offers retry after installation fails", () => {
    const onInstall = vi.fn()
    const items = buildUpdateMenuItems(
      {
        state: "error",
        currentVersion: "1.0.0",
        code: "install-failed",
        version: "9.9.9",
        error: "install failed",
      },
      onInstall,
    )
    expect(items[0]).toMatchObject({ label: "Retry Install Update v9.9.9" })
    ;(items[0] as { click?: () => void }).click?.()
    expect(onInstall).toHaveBeenCalledTimes(1)
  })
})

describe("buildTrayMenuTemplate", () => {
  const actions = {
    showDevsy: vi.fn(),
    showWorkspace: vi.fn(),
    showAllWorkspaces: vi.fn(),
    stopWorkspace: vi.fn(),
    installUpdate: vi.fn(),
    quit: vi.fn(),
  }

  it("shows only active workspaces with open and stop actions", () => {
    const items = buildTrayMenuTemplate(
      {
        activeWorkspaces: [
          { id: "running", status: "running" },
          { id: "busy", status: "busy" },
        ],
        pendingStops: new Set(),
        updateStatus: { state: "idle", currentVersion: "1.0.0" },
      },
      actions,
    )
    expect(items[0]).toMatchObject({ label: "Show Devsy" })
    const submenu = items[2].submenu as Array<Record<string, unknown>>
    expect(submenu[0]).toMatchObject({ label: "running" })
    expect(submenu[1]).toMatchObject({ label: "busy — Busy" })
    expect(submenu[0].submenu).toEqual([
      expect.objectContaining({ label: "Open in Devsy" }),
      expect.objectContaining({ label: "Stop Workspace" }),
    ])
    expect(items.at(-1)).toMatchObject({ label: "Quit Devsy" })
  })

  it("shows an empty state and disables a pending stop", () => {
    const empty = buildTrayMenuTemplate(
      {
        activeWorkspaces: [],
        pendingStops: new Set(),
        updateStatus: { state: "idle", currentVersion: "1.0.0" },
      },
      actions,
    )
    expect((empty[2].submenu as Array<Record<string, unknown>>)[0]).toMatchObject({
      label: "No Active Workspaces",
      enabled: false,
    })

    const pending = buildTrayMenuTemplate(
      {
        activeWorkspaces: [{ id: "ws-1", status: "running" }],
        pendingStops: new Set(["ws-1"]),
        updateStatus: { state: "idle", currentVersion: "1.0.0" },
      },
      actions,
    )
    const stop = ((pending[2].submenu as Array<Record<string, unknown>>)[0]
      .submenu as Array<Record<string, unknown>>)[1]
    expect(stop).toMatchObject({ label: "Stopping…", enabled: false })
  })
})
