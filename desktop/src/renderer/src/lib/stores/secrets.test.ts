import { get } from "svelte/store"
import { beforeEach, describe, expect, it } from "vitest"
import { mockInvoke, resetTauriMocks } from "$lib/__mocks__/tauri.js"

import {
  initSecrets,
  refreshSecrets,
  secrets,
  secretsError,
  secretsLoading,
} from "./secrets.js"

describe("secrets store", () => {
  beforeEach(() => {
    resetTauriMocks()
    secrets.set([])
    secretsError.set(null)
    secretsLoading.set(true)
  })

  it("loads secrets on init", async () => {
    mockInvoke.mockResolvedValue([
      { name: "API_KEY", context: "default" },
      { name: "DB_PW", context: "default", orphaned: true },
    ])

    await initSecrets()

    expect(get(secretsLoading)).toBe(false)
    expect(get(secrets)).toHaveLength(2)
    expect(get(secrets)[1].orphaned).toBe(true)
  })

  it("sets loading false even on error", async () => {
    mockInvoke.mockRejectedValue(new Error("IPC not available"))

    await initSecrets()

    expect(get(secretsLoading)).toBe(false)
    expect(get(secrets)).toEqual([])
    expect(get(secretsError)).toBe("IPC not available")
  })

  it("clears the init error after a successful retry", async () => {
    mockInvoke
      .mockRejectedValueOnce(new Error("IPC not available"))
      .mockResolvedValueOnce([{ name: "TOKEN", context: "default" }])

    await initSecrets()
    expect(get(secretsError)).toBe("IPC not available")

    await initSecrets()
    expect(get(secretsError)).toBeNull()
  })

  it("refreshSecrets updates the store", async () => {
    mockInvoke.mockResolvedValue([{ name: "TOKEN", context: "default" }])

    await refreshSecrets()

    expect(get(secrets)).toHaveLength(1)
    expect(get(secrets)[0].name).toBe("TOKEN")
  })

  it("clears the refresh error after a successful retry", async () => {
    mockInvoke
      .mockRejectedValueOnce(new Error("IPC not available"))
      .mockResolvedValueOnce([{ name: "TOKEN", context: "default" }])

    await refreshSecrets()
    expect(get(secretsError)).toBe("IPC not available")

    await refreshSecrets()
    expect(get(secretsError)).toBeNull()
  })
})
