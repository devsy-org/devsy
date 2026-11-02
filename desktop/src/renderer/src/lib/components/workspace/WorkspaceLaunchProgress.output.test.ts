import { cleanup, fireEvent, render } from "@testing-library/svelte"
import { afterEach, describe, expect, it, vi } from "vitest"
import WorkspaceLaunchProgress from "./WorkspaceLaunchProgress.svelte"

afterEach(cleanup)

const hook = (state: "running" | "succeeded" | "skipped") => ({
  key: "hook",
  operationId: "hook",
  phase: "running_lifecycle_hook",
  state,
  firstSeenAtMs: 0,
})

function mount(
  state: "running" | "succeeded" | "skipped",
  onViewOutput?: () => void,
) {
  return render(WorkspaceLaunchProgress, {
    props: {
      workspaceId: "demo",
      timeline: { operations: [hook(state)] },
      running: false,
      confirmationState: "confirmed" as const,
      elapsedMs: 1000,
      // No streamed lines: availability comes from recorded metadata.
      logsAvailable: false,
      logsOpen: false,
      onToggleLogs: vi.fn(),
      onViewOutput,
    },
  })
}

describe("WorkspaceLaunchProgress view output", () => {
  it.each(["succeeded", "skipped"] as const)(
    "offers output for a %s hook without streamed lines",
    async (state) => {
      const onViewOutput = vi.fn()
      const ui = mount(state, onViewOutput)
      await fireEvent.click(ui.getByRole("button", { name: /View output for/ }))
      expect(onViewOutput).toHaveBeenCalledOnce()
      expect(ui.getByRole("button", { name: "View setup output" })).toBeTruthy()
    },
  )

  it("does not offer output while the hook is still running or when no run is recorded", () => {
    expect(
      mount("running", vi.fn()).queryByRole("button", {
        name: /View output for/,
      }),
    ).toBeNull()
    cleanup()
    const ui = mount("succeeded")
    expect(ui.queryByRole("button", { name: /View output for/ })).toBeNull()
    expect(ui.queryByRole("button", { name: "View setup output" })).toBeNull()
  })
})
