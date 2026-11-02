import { describe, expect, it } from "vitest"
import type { Workspace } from "$lib/types/index.js"
import {
  canonicalSshHost,
  findAliasConflict,
  findLabelConflict,
  preferredSshHost,
  workspaceLabel,
  workspaceMatchesQuery,
} from "./workspaceLabel.js"

const plain: Workspace = { id: "backend" }
const labeled: Workspace = {
  id: "api",
  uid: "u1",
  displayName: "Payments API",
  sshHostAlias: "pay",
}
const platform: Workspace = {
  id: "plat",
  pro: { displayName: "Team Workspace" },
  sshHostAlias: "ignored",
}

describe("workspaceLabel", () => {
  it("falls back to the ID for old payloads", () => {
    expect(workspaceLabel(plain)).toBe("backend")
  })

  it("prefers the display name", () => {
    expect(workspaceLabel(labeled)).toBe("Payments API")
  })

  it("uses the platform display name when no local label is set", () => {
    expect(workspaceLabel(platform)).toBe("Team Workspace")
  })

  it("prefers the platform name over a local label", () => {
    expect(workspaceLabel({ ...platform, displayName: "Local" })).toBe(
      "Team Workspace",
    )
  })

  it("ignores blank labels", () => {
    expect(workspaceLabel({ id: "x", displayName: "   " })).toBe("x")
    expect(workspaceLabel({ id: "x", pro: { displayName: "" } })).toBe("x")
  })
})

describe("ssh hosts", () => {
  it("defaults to the canonical host", () => {
    expect(canonicalSshHost(plain)).toBe("backend.devsy")
    expect(preferredSshHost(plain)).toBe("backend.devsy")
  })

  it("uses the alias as the preferred host", () => {
    expect(canonicalSshHost(labeled)).toBe("api.devsy")
    expect(preferredSshHost(labeled)).toBe("pay.devsy")
  })

  it("honors backend-provided hosts", () => {
    expect(
      preferredSshHost({
        id: "a",
        preferredSSHHost: "x.devsy",
        canonicalSSHHost: "a.devsy",
      }),
    ).toBe("x.devsy")
  })

  it("always uses the canonical host for platform workspaces", () => {
    expect(preferredSshHost(platform)).toBe("plat.devsy")
  })
})

describe("workspaceMatchesQuery", () => {
  it.each([
    ["", true],
    ["payments", true],
    ["API", true],
    ["pay.devsy", true],
    ["pay", true],
    ["nothing", false],
  ])("matches %j -> %s", (query, expected) => {
    expect(workspaceMatchesQuery(labeled, query)).toBe(expected)
  })
})

describe("conflict detection", () => {
  const all = [plain, labeled, platform]

  it("detects duplicate labels case-insensitively against labels and IDs", () => {
    expect(findLabelConflict("payments api", all, "backend")?.id).toBe("api")
    expect(findLabelConflict("Backend", all, "other")?.id).toBe("backend")
    expect(findLabelConflict("Team workspace", all, "other")?.id).toBe("plat")
  })

  it("ignores the workspace being edited", () => {
    expect(findLabelConflict("Payments API", all, "api")).toBeUndefined()
  })

  it("detects alias collisions with canonical and preferred hosts", () => {
    expect(findAliasConflict("pay", all, "backend")?.id).toBe("api")
    expect(findAliasConflict("API", all, "backend")?.id).toBe("api")
    expect(findAliasConflict("fresh", all, "backend")).toBeUndefined()
    expect(findAliasConflict("pay", all, "api")).toBeUndefined()
  })
})
