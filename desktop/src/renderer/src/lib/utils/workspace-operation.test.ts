import { describe, expect, it } from "vitest"
import {
  humanPhase,
  presentWorkspaceStatus,
  type WorkspaceActivity,
  type WorkspaceJob,
  workspaceConfirmedToast,
  workspaceFailedHeadline,
} from "$shared/workspace-operation.js"

function job(partial: Partial<WorkspaceJob>): WorkspaceJob {
  return {
    commandId: "cmd",
    activity: "creating",
    state: "running",
    phase: "cloning_repository",
    ...partial,
  }
}

const ACTIVITIES: WorkspaceActivity[] = [
  "creating",
  "starting",
  "stopping",
  "deleting",
  "rebuilding",
  "resetting",
]

describe("presentWorkspaceStatus", () => {
  it("falls back to the observed lifecycle when no job is active", () => {
    const view = presentWorkspaceStatus({ lifecycle: "Running" })
    expect(view.headline).toBe("Running")
    expect(view.tone).toBe("default")
    expect(view.busy).toBe(false)
    expect(view.phase).toBeUndefined()
  })
  it("shows Checking when neither job nor status exists", () => {
    expect(presentWorkspaceStatus({}).headline).toBe("Checking")
  })
  it("clears the operation layer once the job succeeded", () => {
    const view = presentWorkspaceStatus({
      lifecycle: "Stopped",
      job: job({ state: "succeeded", activity: "stopping" }),
    })
    expect(view.headline).toBe("Stopped")
    expect(view.busy).toBe(false)
  })
  it.each(ACTIVITIES)("shows the bare verb while %s is running", (activity) => {
    const view = presentWorkspaceStatus({
      lifecycle: "Running",
      job: job({ activity }),
    })
    expect(view.headline).toBe(activity[0].toUpperCase() + activity.slice(1))
    expect(view.busy).toBe(true)
    expect(view.tone).toBe("secondary")
    expect(view.phase).toBe("Cloning repository")
  })
  it("keeps the verb headline and says Confirming status while reconciling", () => {
    const view = presentWorkspaceStatus({
      lifecycle: "Running",
      job: job({
        activity: "stopping",
        state: "reconciling",
        phase: "Refreshing status",
      }),
    })
    expect(view.headline).toBe("Stopping")
    expect(view.phase).toBe("Confirming status")
    expect(view.busy).toBe(true)
  })
  it("says Confirming removal for a delete awaiting confirmation", () => {
    const view = presentWorkspaceStatus({
      lifecycle: "Running",
      job: job({
        activity: "deleting",
        state: "reconciling",
        phase: "Refreshing list",
      }),
    })
    expect(view.headline).toBe("Deleting")
    expect(view.phase).toBe("Confirming removal")
    expect(view.recovery).toBeUndefined()
    expect(view.busy).toBe(true)
  })
  it("never claims Deleted before confirmation", () => {
    for (const state of ["running", "reconciling"] as const) {
      const view = presentWorkspaceStatus({
        lifecycle: "Running",
        job: job({ activity: "deleting", state }),
      })
      expect(view.headline).not.toBe("Deleted")
      expect(view.headline).toBe("Deleting")
    }
  })
  it("surfaces a stalled list refresh as recovery, not failure", () => {
    const view = presentWorkspaceStatus({
      lifecycle: "Running",
      job: job({
        activity: "deleting",
        state: "reconciling",
        refreshError: "offline",
      }),
    })
    expect(view.headline).toBe("Deleting")
    expect(view.recovery).toEqual({
      message: "List may be out of date",
      canRetry: true,
    })
    expect(view.error).toBeUndefined()
    expect(view.tone).toBe("warning")
    expect(view.busy).toBe(false)
  })
  it("uses Status may be out of date for non-delete stalls", () => {
    const view = presentWorkspaceStatus({
      job: job({
        activity: "stopping",
        state: "reconciling",
        refreshError: "x",
      }),
    })
    expect(view.recovery?.message).toBe("Status may be out of date")
  })
  it.each(ACTIVITIES)(
    "maps a failed %s to its failure headline",
    (activity) => {
      const view = presentWorkspaceStatus({
        job: job({ activity, state: "failed", error: "boom" }),
      })
      expect(view.headline).toBe(workspaceFailedHeadline(activity))
      expect(view.error).toBe("boom")
      expect(view.tone).toBe("destructive")
      expect(view.busy).toBe(false)
    },
  )
  it("falls back when a failed job has no error message", () => {
    const view = presentWorkspaceStatus({
      job: job({ state: "failed" }),
    })
    expect(view.error).toBe("Workspace operation failed")
    expect(view.tone).toBe("destructive")
  })
  it("marks logs available while a job exists and not otherwise", () => {
    expect(presentWorkspaceStatus({ job: job({}) }).detailsAvailable).toBe(true)
    expect(
      presentWorkspaceStatus({ lifecycle: "Running" }).detailsAvailable,
    ).toBe(false)
  })
})

describe("humanPhase", () => {
  it("maps known CLI phases to customer language", () => {
    expect(humanPhase("cloning_repository")).toBe("Cloning repository")
    expect(humanPhase("building_image")).toBe("Building image")
    expect(humanPhase("starting_container")).toBe("Starting container")
    expect(humanPhase("injecting_agent")).toBe("Connecting agent")
    expect(humanPhase("stopping_workspace")).toBe("Stopping resources")
  })
  it("sentence-cases unknown phases without exposing underscores", () => {
    expect(humanPhase("waiting_for_lock")).toBe("Waiting for lock")
  })
  it("returns undefined for empty input", () => {
    expect(humanPhase(undefined)).toBeUndefined()
    expect(humanPhase("")).toBeUndefined()
  })
})

describe("toast labels", () => {
  it("confirms each operation with locked wording", () => {
    expect(workspaceConfirmedToast("creating")).toBe("Workspace ready")
    expect(workspaceConfirmedToast("starting")).toBe("Workspace running")
    expect(workspaceConfirmedToast("stopping")).toBe("Workspace stopped")
    expect(workspaceConfirmedToast("deleting")).toBe("Workspace deleted")
    expect(workspaceConfirmedToast("rebuilding")).toBe("Workspace rebuilt")
    expect(workspaceConfirmedToast("resetting")).toBe("Workspace reset")
  })
  it("names failed operations", () => {
    expect(workspaceFailedHeadline("deleting")).toBe("Delete failed")
    expect(workspaceFailedHeadline("stopping")).toBe("Stop failed")
  })
})
