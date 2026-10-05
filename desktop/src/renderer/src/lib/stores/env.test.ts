import { get } from "svelte/store"
import { beforeEach, expect, it, vi } from "vitest"

const mocks = vi.hoisted(() => ({ envList: vi.fn() }))
vi.mock("$lib/ipc/commands.js", () => mocks)

import { envError, envLoading, envVars, initEnv, refreshEnv } from "./env.js"

beforeEach(() => {
  vi.resetAllMocks()
  envVars.set([])
  envError.set(null)
})
it("surfaces load failures and clears the error after retry", async () => {
  mocks.envList
    .mockRejectedValueOnce(new Error("offline"))
    .mockResolvedValueOnce([])
  await initEnv()
  expect(get(envError)).toBe("offline")
  expect(get(envLoading)).toBe(false)
  await refreshEnv()
  expect(get(envError)).toBeNull()
})
it("ignores an older load that finishes after a newer request", async () => {
  let resolveOld!: (rows: unknown[]) => void
  mocks.envList
    .mockReturnValueOnce(
      new Promise((resolve) => {
        resolveOld = resolve
      }),
    )
    .mockResolvedValueOnce([])
  const old = refreshEnv()
  await refreshEnv()
  resolveOld([
    { name: "OLD", context: "default", value: "old", attached: false },
  ])
  await old
  expect(get(envVars)).toEqual([])
})
