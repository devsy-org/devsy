import { get } from "svelte/store"
import { beforeEach, describe, expect, it } from "vitest"

import {
  addTerminal,
  clearTerminals,
  removeTerminal,
  terminalCount,
  terminals,
} from "./terminals.js"

describe("terminals store", () => {
  beforeEach(() => {
    terminals.set([])
  })

  it("starts with empty terminals", () => {
    expect(get(terminals)).toEqual([])
    expect(get(terminalCount)).toBe(0)
  })

  it("addTerminal adds a session", () => {
    addTerminal({ id: "t-1", label: "Shell", type: "shell" })

    const current = get(terminals)
    expect(current).toHaveLength(1)
    expect(current[0].id).toBe("t-1")
    expect(current[0].type).toBe("shell")
  })

  it("addTerminal adds multiple sessions", () => {
    addTerminal({ id: "t-1", label: "Shell", type: "shell" })
    addTerminal({
      id: "t-2",
      label: "SSH: ws-1",
      type: "ssh",
      workspaceId: "ws-1",
    })

    expect(get(terminals)).toHaveLength(2)
    expect(get(terminalCount)).toBe(2)
  })

  it("removeTerminal removes by id", () => {
    addTerminal({ id: "t-1", label: "Shell", type: "shell" })
    addTerminal({ id: "t-2", label: "Shell 2", type: "shell" })

    removeTerminal("t-1")

    const current = get(terminals)
    expect(current).toHaveLength(1)
    expect(current[0].id).toBe("t-2")
  })

  it("removeTerminal is safe with non-existent id", () => {
    addTerminal({ id: "t-1", label: "Shell", type: "shell" })

    removeTerminal("non-existent")

    expect(get(terminals)).toHaveLength(1)
  })

  it("clearTerminals removes all sessions", () => {
    addTerminal({ id: "t-1", label: "Shell", type: "shell" })
    addTerminal({ id: "t-2", label: "Shell 2", type: "shell" })

    clearTerminals()

    expect(get(terminals)).toEqual([])
    expect(get(terminalCount)).toBe(0)
  })

  it("terminalCount is derived from terminals length", () => {
    expect(get(terminalCount)).toBe(0)

    addTerminal({ id: "t-1", label: "Shell", type: "shell" })
    expect(get(terminalCount)).toBe(1)

    addTerminal({ id: "t-2", label: "Shell 2", type: "shell" })
    expect(get(terminalCount)).toBe(2)

    removeTerminal("t-1")
    expect(get(terminalCount)).toBe(1)
  })

  it("preserves workspaceId on ssh sessions", () => {
    addTerminal({
      id: "t-1",
      label: "SSH: my-ws",
      type: "ssh",
      workspaceId: "my-ws",
    })

    const session = get(terminals)[0]
    expect(session.workspaceId).toBe("my-ws")
    expect(session.type).toBe("ssh")
  })
})
