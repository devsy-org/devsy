import { describe, expect, it, vi } from "vitest"
import type { WorkspaceJob } from "../../shared/workspace-operation.js"
import {
  collectTerminalOutcomes,
  type NotificationRequest,
  outcomeNotificationBody,
  outcomeNotifies,
  TrayNotifier,
  updateNotifies,
} from "../tray-notifications.js"
import type { UpdateStatus } from "../updater.js"

function job(partial: Partial<WorkspaceJob>): WorkspaceJob {
  return {
    commandId: "cmd-1",
    activity: "starting",
    state: "running",
    phase: "Preparing",
    ...partial,
  }
}

describe("collectTerminalOutcomes", () => {
  it("emits an outcome when a job reaches a terminal state", () => {
    const previous = { ws: job({ state: "running" }) }
    const current = { ws: job({ state: "succeeded" }) }
    expect(collectTerminalOutcomes(previous, current, new Set())).toEqual([
      {
        workspaceId: "ws",
        commandId: "cmd-1",
        activity: "starting",
        state: "succeeded",
      },
    ])
  })

  it("does not emit for non-terminal states", () => {
    const current = { ws: job({ state: "reconciling", refreshError: "x" }) }
    expect(collectTerminalOutcomes({}, current, new Set())).toEqual([])
  })

  it("does not re-emit for an already terminal command", () => {
    const previous = { ws: job({ state: "failed", error: "boom" }) }
    const current = { ws: job({ state: "failed", error: "boom" }) }
    expect(collectTerminalOutcomes(previous, current, new Set())).toEqual([])
  })

  it("skips commands already notified", () => {
    const current = { ws: job({ state: "failed", error: "boom" }) }
    expect(collectTerminalOutcomes({}, current, new Set(["cmd-1"]))).toEqual([])
  })

  it("treats a new command id as a new outcome", () => {
    const previous = { ws: job({ state: "failed", error: "boom" }) }
    const current = {
      ws: job({ commandId: "cmd-2", state: "failed", error: "boom" }),
    }
    const outcomes = collectTerminalOutcomes(
      previous,
      current,
      new Set(["cmd-1"]),
    )
    expect(outcomes).toHaveLength(1)
    expect(outcomes[0].commandId).toBe("cmd-2")
  })
})

describe("outcomeNotifies", () => {
  const failed = {
    workspaceId: "ws",
    commandId: "c",
    activity: "stopping" as const,
    state: "failed" as const,
  }
  const succeeded = { ...failed, state: "succeeded" as const }

  it("notifies nothing when off", () => {
    expect(outcomeNotifies(failed, "off")).toBe(false)
    expect(outcomeNotifies(succeeded, "off")).toBe(false)
  })

  it("notifies only failures by default", () => {
    expect(outcomeNotifies(failed, "failures")).toBe(true)
    expect(outcomeNotifies(succeeded, "failures")).toBe(false)
  })

  it("notifies all terminal outcomes when set to all", () => {
    expect(outcomeNotifies(failed, "all")).toBe(true)
    expect(outcomeNotifies(succeeded, "all")).toBe(true)
  })
})

describe("outcomeNotificationBody", () => {
  it("uses text verbs", () => {
    expect(
      outcomeNotificationBody({
        workspaceId: "api",
        commandId: "c",
        activity: "stopping",
        state: "succeeded",
      }),
    ).toBe("api: Stop completed")
    expect(
      outcomeNotificationBody({
        workspaceId: "api",
        commandId: "c",
        activity: "starting",
        state: "failed",
      }),
    ).toBe("api: Start failed")
  })
})

describe("updateNotifies", () => {
  const downloaded: UpdateStatus = {
    state: "downloaded",
    currentVersion: "1.0.0",
    availableVersion: "1.1.0",
  }

  it("notifies downloaded updates only at the all level, once per version", () => {
    const first = updateNotifies(downloaded, "all", undefined)
    expect(first.notifies).toBe(true)
    expect(first.version).toBe("1.1.0")
    expect(updateNotifies(downloaded, "all", "1.1.0").notifies).toBe(false)
    expect(updateNotifies(downloaded, "failures", undefined).notifies).toBe(
      false,
    )
  })

  it("never notifies when off", () => {
    expect(updateNotifies(downloaded, "off", undefined).notifies).toBe(false)
  })

  it("notifies for install failures once per version", () => {
    const failed: UpdateStatus = {
      state: "error",
      currentVersion: "1.0.0",
      availableVersion: "1.1.0",
      code: "install-failed",
      error: "x",
    }
    expect(updateNotifies(failed, "failures", undefined).notifies).toBe(true)
    expect(
      updateNotifies(failed, "failures", "install-failed:1.1.0").notifies,
    ).toBe(false)
  })

  it("allows an install failure after the update-ready notification", () => {
    const failed: UpdateStatus = {
      state: "error",
      currentVersion: "1.0.0",
      availableVersion: "1.1.0",
      code: "install-failed",
      error: "x",
    }
    const ready = updateNotifies(downloaded, "all", undefined)
    expect(updateNotifies(failed, "failures", ready.key).notifies).toBe(true)
    expect(
      updateNotifies(failed, "failures", "install-failed:1.1.0").notifies,
    ).toBe(false)
  })

  it("ignores other update states", () => {
    expect(
      updateNotifies(
        { state: "checking", currentVersion: "1.0.0" },
        "all",
        undefined,
      ).notifies,
    ).toBe(false)
  })
})

function makeNotifier(
  level: () => "off" | "failures" | "all",
  focused: () => boolean,
) {
  const sent: NotificationRequest[] = []
  const notifier = new TrayNotifier({
    getLevel: level,
    isAppFocused: focused,
    sink: (request) => sent.push(request),
    openWorkspace: vi.fn(),
    openWorkspaceLogs: vi.fn(),
    openUpdates: vi.fn(),
  })
  return { notifier, sent }
}

describe("TrayNotifier", () => {
  it("sends a notification for a failure under the default level", () => {
    const { notifier, sent } = makeNotifier(
      () => "failures",
      () => false,
    )
    notifier.onJobsChanged({ ws: job({ state: "running" }) })
    notifier.onJobsChanged({ ws: job({ state: "failed", error: "boom" }) })
    expect(sent).toHaveLength(1)
    expect(sent[0].body).toBe("ws: Start failed")
  })

  it("never notifies for jobs recovered in the initial snapshot", () => {
    const { notifier, sent } = makeNotifier(
      () => "all",
      () => false,
    )
    const terminal = { ws: job({ state: "failed", error: "boom" }) }
    notifier.onJobsChanged(terminal)
    notifier.onJobsChanged(terminal)
    expect(sent).toHaveLength(0)
  })

  it("deduplicates repeated snapshots of the same command after hydration", () => {
    const { notifier, sent } = makeNotifier(
      () => "failures",
      () => false,
    )
    const terminal = { ws: job({ state: "failed", error: "boom" }) }
    notifier.onJobsChanged({ ws: job({ state: "running" }) })
    notifier.onJobsChanged(terminal)
    notifier.onJobsChanged(terminal)
    expect(sent).toHaveLength(1)
  })

  it("suppresses notifications while the app is focused", () => {
    const { notifier, sent } = makeNotifier(
      () => "failures",
      () => true,
    )
    notifier.onJobsChanged({ ws: job({ state: "running" }) })
    notifier.onJobsChanged({ ws: job({ state: "failed", error: "boom" }) })
    expect(sent).toHaveLength(0)
  })

  it("routes failure clicks to logs and success clicks to the workspace", () => {
    const openWorkspace = vi.fn()
    const openWorkspaceLogs = vi.fn()
    const sent: NotificationRequest[] = []
    const notifier = new TrayNotifier({
      getLevel: () => "all",
      isAppFocused: () => false,
      sink: (request) => sent.push(request),
      openWorkspace,
      openWorkspaceLogs,
      openUpdates: vi.fn(),
    })
    notifier.onJobsChanged({ ws: job({ state: "running" }) })
    notifier.onJobsChanged({ ws: job({ state: "failed", error: "boom" }) })
    sent[0].onClick()
    expect(openWorkspaceLogs).toHaveBeenCalledWith("ws")
    notifier.onJobsChanged({
      ws: job({ commandId: "cmd-2", state: "succeeded" }),
    })
    sent[1].onClick()
    expect(openWorkspace).toHaveBeenCalledWith("ws")
  })
})
