import { describe, expect, it, vi } from "vitest"
import {
  buildTrayMenuTemplate,
  buildUpdateMenuItems,
  countRunningWorkspaces,
} from "../tray.js"
import type { WorkspaceJob } from "../../shared/workspace-operation.js"

vi.mock("electron", () => ({}))
vi.mock("../updater.js", () => ({
  getLastStatus: () => ({ state: "idle" }),
  installUpdate: vi.fn(),
  onUpdateStatusChanged: vi.fn(() => () => {}),
}))

describe("buildUpdateMenuItems", () => {
  it("returns nothing when no update is downloaded", () => {
    expect(
      buildUpdateMenuItems(
        { state: "idle", currentVersion: "1.0.0" },
        () => {},
      ),
    ).toEqual([])
    expect(
      buildUpdateMenuItems(
        { state: "checking", currentVersion: "1.0.0" },
        () => {},
      ),
    ).toEqual([])
    expect(
      buildUpdateMenuItems(
        {
          state: "available",
          currentVersion: "1.0.0",
          availableVersion: "1.1.0",
        },
        () => {},
      ),
    ).toEqual([])
    expect(
      buildUpdateMenuItems(
        {
          state: "downloading",
          currentVersion: "1.0.0",
          availableVersion: "1.1.0",
          progress: {
            percent: 50,
            bytesPerSecond: 1000,
            transferred: 50,
            total: 100,
          },
        },
        () => {},
      ),
    ).toEqual([])
    expect(
      buildUpdateMenuItems(
        { state: "not-available", currentVersion: "1.0.0" },
        () => {},
      ),
    ).toEqual([])
    expect(
      buildUpdateMenuItems(
        { state: "up-to-date", currentVersion: "1.0.0" },
        () => {},
      ),
    ).toEqual([])
    expect(
      buildUpdateMenuItems(
        {
          state: "error",
          currentVersion: "1.0.0",
          error: "x",
          code: "network",
        },
        () => {},
      ),
    ).toEqual([])
  })

  it("adds Update item + separator when downloaded", () => {
    const onInstall = vi.fn()
    const items = buildUpdateMenuItems(
      {
        state: "downloaded",
        currentVersion: "1.0.0",
        availableVersion: "9.9.9",
      },
      onInstall,
    )
    expect(items).toHaveLength(2)
    expect(items[0]).toMatchObject({ label: "Update to 9.9.9" })
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
    expect(items[0]).toMatchObject({ label: "Restart" })
  })

  it("offers retry after installation fails", () => {
    const onInstall = vi.fn()
    const items = buildUpdateMenuItems(
      {
        state: "error",
        currentVersion: "1.0.0",
        code: "install-failed",
        availableVersion: "9.9.9",
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
  const settings = {
    runAtStartup: true,
    openToTrayOnStartup: false,
    trayNotifications: "failures" as const,
  }

  function makeActions() {
    return {
      showDevsy: vi.fn(),
      showWorkspace: vi.fn(),
      showWorkspaceLogs: vi.fn(),
      showAllWorkspaces: vi.fn(),
      showSettings: vi.fn(),
      startWorkspace: vi.fn(),
      stopWorkspace: vi.fn(),
      toggleRunAtStartup: vi.fn(),
      toggleOpenToTray: vi.fn(),
      installUpdate: vi.fn(),
      quit: vi.fn(),
    }
  }

  function model(partial: Partial<Parameters<typeof buildTrayMenuTemplate>[0]>) {
    return {
      workspaces: [],
      pendingStops: new Set<string>(),
      pendingStarts: new Set<string>(),
      updateStatus: { state: "idle" as const, currentVersion: "1.0.0" },
      settings,
      ...partial,
    }
  }

  function labels(items: Electron.MenuItemConstructorOptions[]): (string | undefined)[] {
    return items.map((item) => item.label)
  }

  it("shows an empty state when there are no workspaces", () => {
    const items = buildTrayMenuTemplate(model({}), makeActions())
    expect(labels(items)).toContain("No Workspaces")
  })

  it("shows the running count in the header", () => {
    const items = buildTrayMenuTemplate(
      model({
        workspaces: [
          { id: "a", status: "running" },
          { id: "b", status: "running" },
          { id: "c", status: "stopped" },
        ],
      }),
      makeActions(),
    )
    expect(items[0]).toMatchObject({
      label: "Devsy — 2 running workspaces",
      enabled: false,
    })
  })

  it("counts JSON-encoded statuses and jobs the same as tray rows", () => {
    const job = (partial: Partial<WorkspaceJob>): WorkspaceJob => ({
      commandId: "cmd-1",
      activity: "starting",
      state: "running",
      phase: "Preparing",
      ...partial,
    })
    expect(
      countRunningWorkspaces(
        [
          { id: "a", status: '{"state":"running"}' },
          { id: "b", status: "running" },
          { id: "c", status: "stopped" },
          { id: "d", status: "running" },
        ],
        { c: job({}), d: job({ state: "failed", error: "x" }) },
      ),
    ).toBe(3)
  })

  it("limits the list to five most recently used workspaces with an overflow link", () => {
    const workspaces = Array.from({ length: 7 }, (_, i) => ({
      id: `ws-${i}`,
      status: "stopped",
    }))
    const actions = makeActions()
    const items = buildTrayMenuTemplate(model({ workspaces }), actions)
    const rows = items.filter((item) => item.label?.startsWith("○"))
    expect(rows).toHaveLength(5)
    const overflow = items.find((item) =>
      item.label?.includes("View All 7 Workspaces in Devsy"),
    )
    expect(overflow).toBeDefined()
    ;(overflow as { click?: () => void }).click?.()
    expect(actions.showAllWorkspaces).toHaveBeenCalledTimes(1)
  })

  it("does not add an overflow link at or under the limit", () => {
    const workspaces = Array.from({ length: 5 }, (_, i) => ({
      id: `ws-${i}`,
      status: "stopped",
    }))
    const items = buildTrayMenuTemplate(model({ workspaces }), makeActions())
    expect(
      items.some((item) => item.label?.includes("View All")),
    ).toBe(false)
  })

  it("exposes Stop for running workspaces with text labels and state glyphs", () => {
    const actions = makeActions()
    const items = buildTrayMenuTemplate(
      model({ workspaces: [{ id: "api", status: "running" }] }),
      actions,
    )
    const row = items.find((item) => item.label === "● api — Running")
    expect(row).toBeDefined()
    const submenu = row?.submenu as Electron.MenuItemConstructorOptions[]
    const stop = submenu.find((item) => item.label === "Stop Workspace")
    expect(stop).toBeDefined()
    expect(stop?.enabled).not.toBe(false)
    ;(stop as { click?: () => void }).click?.()
    expect(actions.stopWorkspace).toHaveBeenCalledWith("api")
  })

  it("exposes Start for stopped workspaces", () => {
    const actions = makeActions()
    const items = buildTrayMenuTemplate(
      model({ workspaces: [{ id: "api", status: "stopped" }] }),
      actions,
    )
    const row = items.find((item) => item.label === "○ api — Stopped")
    const submenu = row?.submenu as Electron.MenuItemConstructorOptions[]
    const start = submenu.find((item) => item.label === "Start Workspace")
    expect(start).toBeDefined()
    ;(start as { click?: () => void }).click?.()
    expect(actions.startWorkspace).toHaveBeenCalledWith("api")
  })

  it("offers View Logs for failed, busy, and unknown rows instead of lifecycle actions", () => {
    const items = buildTrayMenuTemplate(
      model({
        workspaces: [
          { id: "failed-ws", status: "stopped" },
          { id: "busy-ws", status: "busy" },
          { id: "unknown-ws" },
        ],
        jobs: {
          "failed-ws": {
            commandId: "c1",
            activity: "stopping",
            state: "failed",
            phase: "See workspace logs for details",
            error: "boom",
          },
          "busy-ws": {
            commandId: "c2",
            activity: "starting",
            state: "running",
            phase: "Building",
          },
        },
      }),
      makeActions(),
    )
    const row = (name: string) =>
      items.find((item) => item.label?.includes(name))
        ?.submenu as Electron.MenuItemConstructorOptions[]
    expect(row("failed-ws").some((item) => item.label === "View Logs")).toBe(true)
    expect(row("failed-ws").some((item) => item.label === "Stop Workspace")).toBe(false)
    expect(row("busy-ws").some((item) => item.label === "View Logs")).toBe(true)
    expect(row("busy-ws").some((item) => item.label === "Start Workspace")).toBe(false)
    expect(row("unknown-ws").some((item) => item.label === "View Logs")).toBe(true)
  })

  it("marks failed rows with a failed glyph and text", () => {
    const items = buildTrayMenuTemplate(
      model({
        workspaces: [{ id: "api", status: "stopped" }],
        jobs: {
          api: {
            commandId: "c1",
            activity: "stopping",
            state: "failed",
            phase: "See workspace logs for details",
            error: "boom",
          },
        },
      }),
      makeActions(),
    )
    expect(items.some((item) => item.label === "✖ api — Stop failed")).toBe(true)
  })

  it("disables Stop while a stop is pending", () => {
    const items = buildTrayMenuTemplate(
      model({
        workspaces: [{ id: "api", status: "running" }],
        pendingStops: new Set(["api"]),
      }),
      makeActions(),
    )
    const row = items.find((item) => item.label?.includes("api"))
    const submenu = row?.submenu as Electron.MenuItemConstructorOptions[]
    const stop = submenu.find((item) => item.label?.includes("Stop"))
    expect(stop?.enabled).toBe(false)
  })

  it("disables Start while a start is pending", () => {
    const items = buildTrayMenuTemplate(
      model({
        workspaces: [{ id: "api", status: "stopped" }],
        pendingStarts: new Set(["api"]),
      }),
      makeActions(),
    )
    const row = items.find((item) => item.label === "○ api — Stopped")
    const submenu = row?.submenu as Electron.MenuItemConstructorOptions[]
    const start = submenu.find((item) => item.label?.includes("Start"))
    expect(start?.enabled).toBe(false)
    expect(start?.click).toBeUndefined()
  })

  it("mirrors the startup toggles as native checkboxes", () => {
    const actions = makeActions()
    const items = buildTrayMenuTemplate(
      model({
        settings: {
          runAtStartup: true,
          openToTrayOnStartup: false,
          trayNotifications: "failures",
        },
      }),
      actions,
    )
    const prefs = items.find((item) => item.label === "Preferences")
    const submenu = prefs?.submenu as Electron.MenuItemConstructorOptions[]
    const runAtStartup = submenu.find((item) => item.label === "Run at Startup")
    const openToTray = submenu.find(
      (item) => item.label === "Open to Tray on Startup",
    )
    expect(runAtStartup).toMatchObject({ type: "checkbox", checked: true })
    expect(openToTray).toMatchObject({
      type: "checkbox",
      checked: false,
      enabled: true,
    })
    ;(runAtStartup as { click?: () => void }).click?.()
    expect(actions.toggleRunAtStartup).toHaveBeenCalledTimes(1)
    ;(openToTray as { click?: () => void }).click?.()
    expect(actions.toggleOpenToTray).toHaveBeenCalledTimes(1)
  })

  it("disables the dependent toggle when run at startup is off", () => {
    const items = buildTrayMenuTemplate(
      model({
        settings: {
          runAtStartup: false,
          openToTrayOnStartup: false,
          trayNotifications: "failures",
        },
      }),
      makeActions(),
    )
    const prefs = items.find((item) => item.label === "Preferences")
    const submenu = prefs?.submenu as Electron.MenuItemConstructorOptions[]
    expect(
      submenu.find((item) => item.label === "Open to Tray on Startup")?.enabled,
    ).toBe(false)
  })

  it("links into DevSy settings from the preferences submenu", () => {
    const actions = makeActions()
    const items = buildTrayMenuTemplate(model({}), actions)
    const prefs = items.find((item) => item.label === "Preferences")
    const submenu = prefs?.submenu as Electron.MenuItemConstructorOptions[]
    const openSettings = submenu.find((item) => item.label === "Open Settings…")
    ;(openSettings as { click?: () => void }).click?.()
    expect(actions.showSettings).toHaveBeenCalledTimes(1)
  })

  it("keeps update items and Quit", () => {
    const items = buildTrayMenuTemplate(
      model({
        updateStatus: {
          state: "downloaded",
          currentVersion: "1.0.0",
          availableVersion: "9.9.9",
        },
      }),
      makeActions(),
    )
    expect(labels(items)).toContain("Update to 9.9.9")
    expect(labels(items)).toContain("Quit Devsy")
  })
})

describe("trayWorkspaceState", () => {
  it("maps statuses and jobs to row states", async () => {
    const { trayWorkspaceState } = await import("../tray.js")
    expect(trayWorkspaceState({ id: "a", status: "running" })).toBe("running")
    expect(trayWorkspaceState({ id: "a", status: "stopped" })).toBe("stopped")
    expect(
      trayWorkspaceState({ id: "a", status: '{"state":"stopped"}' }),
    ).toBe("stopped")
    expect(trayWorkspaceState({ id: "a" })).toBe("unknown")
    expect(
      trayWorkspaceState({ id: "a", status: "stopped" }, {
        commandId: "c",
        activity: "starting",
        state: "running",
        phase: "Building",
      }),
    ).toBe("busy")
    expect(
      trayWorkspaceState({ id: "a", status: "stopped" }, {
        commandId: "c",
        activity: "starting",
        state: "failed",
        phase: "",
        error: "x",
      }),
    ).toBe("failed")
  })
})
