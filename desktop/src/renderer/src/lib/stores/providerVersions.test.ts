import { get } from "svelte/store"
import { beforeEach, describe, expect, it, vi } from "vitest"

vi.mock("$lib/ipc/commands.js", () => ({
  providerListVersions: vi.fn(),
  providerGetUpdateCache: vi.fn(),
  providerCheckUpdates: vi.fn(),
}))

import {
  providerCheckUpdates,
  providerGetUpdateCache,
  providerListVersions,
} from "$lib/ipc/commands.js"
import {
  loadCachedUpdates,
  loadVersionsFor,
  providerVersions,
  refreshUpdates,
  resetProviderVersionsStore,
} from "./providerVersions.js"

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

  it("renders cached results before a live refresh", async () => {
    vi.mocked(providerGetUpdateCache).mockResolvedValue({
      updates: {
        docker: {
          current: "v1.0",
          latest: "v1.1",
          updateAvailable: true,
          unsupported: false,
        },
      },
      lastCheckedAt: "2026-09-21T17:00:00.000Z",
    })

    await loadCachedUpdates()

    const state = get(providerVersions)
    expect(state.updates.docker.latest).toBe("v1.1")
    expect(state.lastCheckedAt?.toISOString()).toBe("2026-09-21T17:00:00.000Z")
  })

  it("keeps cached results and exposes partial check failures", async () => {
    vi.mocked(providerGetUpdateCache).mockResolvedValue({
      updates: {
        docker: {
          current: "v1.0",
          latest: "v1.1",
          updateAvailable: true,
          unsupported: false,
        },
      },
      lastCheckedAt: "2026-09-21T17:00:00.000Z",
    })
    await loadCachedUpdates()
    vi.mocked(providerCheckUpdates).mockResolvedValue({
      docker: {
        current: "v1.0",
        latest: "",
        updateAvailable: false,
        unsupported: false,
        error: "network unavailable",
      },
    })

    await refreshUpdates()

    const state = get(providerVersions)
    expect(state.updates.docker.latest).toBe("v1.1")
    expect(state.updates.docker.updateAvailable).toBe(true)
    expect(state.updates.docker.error).toBe("network unavailable")
    expect(state.refreshError).toBe("One provider update check failed.")
    expect(state.refreshing).toBe(false)
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
