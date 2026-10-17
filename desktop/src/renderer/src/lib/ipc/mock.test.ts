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
