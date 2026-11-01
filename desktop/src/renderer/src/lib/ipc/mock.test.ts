import { describe, expect, it } from "vitest"
import type { Provider } from "$lib/types/index.js"
import { invoke } from "./mock.js"

describe("browser IPC mock", () => {
  it("returns a stable provider catalog without changing installed providers", async () => {
    const installedBefore = structuredClone(
      await invoke<Provider[]>("provider_list"),
    )

    const firstCatalog = await invoke<string[]>("provider_available")
    const secondCatalog = await invoke<string[]>("provider_available")
    const installedAfter = await invoke<Provider[]>("provider_list")

    expect(firstCatalog).toEqual(["docker", "terraform"])
    expect(secondCatalog).toEqual(firstCatalog)
    expect(installedAfter).toEqual(installedBefore)
    expect(
      installedAfter.find(({ name }) => name === "docker")?.isDefault,
    ).toBe(true)
  })
})

describe("browser IPC mock workspace edit", () => {
  it("applies a label and alias and rejects duplicates", async () => {
    const saved = await invoke<{
      ok: boolean
      result?: { preferredSSHHost: string; effectiveDisplayName: string }
    }>("workspace_edit", {
      workspaceId: "my-project",
      expectedUid: "ws-001",
      displayName: "Primary",
      sshHostAlias: "primary",
    })
    expect(saved.ok).toBe(true)
    expect(saved.result?.preferredSSHHost).toBe("primary.devsy")
    expect(saved.result?.effectiveDisplayName).toBe("Primary")

    const labelClash = await invoke<{ ok: boolean; field?: string }>(
      "workspace_edit",
      {
        workspaceId: "backend-api",
        expectedUid: "ws-002",
        displayName: "primary",
      },
    )
    expect(labelClash).toMatchObject({
      ok: false,
      field: "displayName",
      code: "conflict",
      conflict: "my-project",
    })

    const aliasClash = await invoke("workspace_edit", {
      workspaceId: "backend-api",
      expectedUid: "ws-002",
      sshHostAlias: "primary",
    })
    expect(aliasClash).toMatchObject({ ok: false, field: "sshHostAlias" })
  })

  it("reports a stale UID as not found", async () => {
    expect(
      await invoke("workspace_edit", {
        workspaceId: "ml-notebook",
        expectedUid: "different",
        displayName: "x",
      }),
    ).toMatchObject({ ok: false, field: "workspace", code: "not-found" })
  })
})
