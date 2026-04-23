import { describe, it, expect } from "vitest"
import { DaemonState } from "../state.js"
import type { Workspace, Provider, Machine, Context } from "../state.js"

function makeWorkspace(id: string, lastUsed: string): Workspace {
  return { id, lastUsed: lastUsed }
}

function makeProvider(name: string): Provider {
  return { name }
}

function makeMachine(id: string): Machine {
  return { id }
}

describe("DaemonState", () => {
  it("detects workspace changes", () => {
    const state = new DaemonState()
    const ws = [makeWorkspace("ws1", "2024-01-01")]
    expect(state.updateWorkspaces(ws)).toBe(true)
    expect(state.updateWorkspaces(ws)).toBe(false)
  })

  it("detects workspace removal", () => {
    const state = new DaemonState()
    expect(
      state.updateWorkspaces([
        makeWorkspace("ws1", "2024-01-01"),
        makeWorkspace("ws2", "2024-01-02"),
      ]),
    ).toBe(true)
    expect(state.updateWorkspaces([makeWorkspace("ws1", "2024-01-01")])).toBe(true)
    expect(state.workspaceList()).toHaveLength(1)
  })

  it("detects provider changes", () => {
    const state = new DaemonState()
    const providers = [makeProvider("docker")]
    expect(state.updateProviders(providers)).toBe(true)
    expect(state.updateProviders(providers)).toBe(false)
    expect(state.updateProviders([makeProvider("docker"), makeProvider("kubernetes")])).toBe(true)
  })

  it("sorts workspaces by lastUsed descending", () => {
    const state = new DaemonState()
    state.updateWorkspaces([
      makeWorkspace("old", "2024-01-01"),
      makeWorkspace("new", "2024-06-01"),
      makeWorkspace("mid", "2024-03-01"),
    ])
    const sorted = state.workspaceList()
    expect(sorted[0].id).toBe("new")
    expect(sorted[1].id).toBe("mid")
    expect(sorted[2].id).toBe("old")
  })

  it("sorts providers by name ascending", () => {
    const state = new DaemonState()
    state.updateProviders([makeProvider("zebra"), makeProvider("alpha"), makeProvider("middle")])
    const sorted = state.providerList()
    expect(sorted[0].name).toBe("alpha")
    expect(sorted[1].name).toBe("middle")
    expect(sorted[2].name).toBe("zebra")
  })

  it("sorts machines by id ascending", () => {
    const state = new DaemonState()
    state.updateMachines([makeMachine("z-machine"), makeMachine("a-machine")])
    const sorted = state.machineList()
    expect(sorted[0].id).toBe("a-machine")
    expect(sorted[1].id).toBe("z-machine")
  })

  it("detects context changes", () => {
    const state = new DaemonState()
    const contexts: Context[] = [{ name: "default" }, { name: "staging" }]
    expect(state.updateContexts(contexts, "default")).toBe(true)
    expect(state.updateContexts(contexts, "default")).toBe(false)
    expect(state.updateContexts(contexts, "staging")).toBe(true)
  })
})
