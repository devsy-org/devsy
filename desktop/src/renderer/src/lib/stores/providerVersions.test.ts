import { describe, it, expect, vi, beforeEach } from "vitest"
import { get } from "svelte/store"

vi.mock("$lib/ipc/commands.js", () => ({
  providerListVersions: vi.fn(),
  providerCheckUpdates: vi.fn(),
}))

import {
  providerVersions,
  refreshUpdates,
  loadVersionsFor,
  resetProviderVersionsStore,
} from "./providerVersions.js"
import {
  providerListVersions,
  providerCheckUpdates,
} from "$lib/ipc/commands.js"

describe("providerVersions store", () => {
  beforeEach(() => {
    resetProviderVersionsStore()
    vi.clearAllMocks()
  })

  it("seeds via refreshUpdates", async () => {
    vi.mocked(providerCheckUpdates).mockResolvedValue({
      aws: {
        current: "v1.0",
        latest: "v1.1",
        updateAvailable: true,
        unsupported: false,
      },
    })
    await refreshUpdates()
    const state = get(providerVersions)
    expect(state.updates.aws.updateAvailable).toBe(true)
    expect(state.lastCheckedAt).not.toBeNull()
  })

  it("populates byProvider via loadVersionsFor", async () => {
    vi.mocked(providerListVersions).mockResolvedValue({
      versions: [
        {
          tag: "v2.0",
          publishedAt: "2026-01-01T00:00:00Z",
          prerelease: false,
          current: true,
        },
      ],
      unsupported: false,
    })
    await loadVersionsFor("gcp")
    const state = get(providerVersions)
    expect(state.byProvider.gcp.versions).toHaveLength(1)
    expect(state.byProvider.gcp.versions[0].tag).toBe("v2.0")
    expect(state.byProvider.gcp.unsupported).toBe(false)
  })

  it("resets via resetProviderVersionsStore", async () => {
    vi.mocked(providerListVersions).mockResolvedValue({
      versions: [
        {
          tag: "v2.0",
          publishedAt: "2026-01-01T00:00:00Z",
          prerelease: false,
          current: true,
        },
      ],
      unsupported: false,
    })
    await loadVersionsFor("gcp")
    resetProviderVersionsStore()
    const state = get(providerVersions)
    expect(state.byProvider).toEqual({})
    expect(state.updates).toEqual({})
    expect(state.lastCheckedAt).toBeNull()
  })
})
