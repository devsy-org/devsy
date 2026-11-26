import { get } from "svelte/store"
import { beforeEach, describe, expect, it } from "vitest"
import { mockInvoke, resetTauriMocks } from "$lib/__mocks__/tauri.js"
import type { SecretProtectionStatus } from "$lib/types/index.js"
import {
  initSecretProtection,
  refreshSecretProtection,
  secretProtectionError,
  secretProtectionLoading,
  secretProtectionStatus,
} from "./secret-protection.js"

const status: SecretProtectionStatus = {
  availability: "locked",
  keySource: "passphrase",
  remembered: false,
  rememberedAvailable: true,
  fileEntries: [],
  sessionUnlocked: false,
}
describe("secret protection store", () => {
  beforeEach(() => {
    resetTauriMocks()
    secretProtectionStatus.set(null)
    secretProtectionError.set(null)
    secretProtectionLoading.set(false)
  })
  it("loads metadata and exposes failures with retry", async () => {
    mockInvoke
      .mockRejectedValueOnce(new Error("raw text"))
      .mockResolvedValueOnce(status)
    await initSecretProtection()
    expect(get(secretProtectionError)).toBe(
      "Unable to load secret security status.",
    )
    expect(get(secretProtectionLoading)).toBe(false)
    await refreshSecretProtection()
    expect(get(secretProtectionStatus)).toEqual(status)
    expect(get(secretProtectionError)).toBeNull()
  })
  it("ignores an older refresh completing after the latest request", async () => {
    let resolveOlder!: (value: SecretProtectionStatus) => void
    mockInvoke
      .mockImplementationOnce(
        () =>
          new Promise<SecretProtectionStatus>((resolve) => {
            resolveOlder = resolve
          }),
      )
      .mockResolvedValueOnce(status)
    const older = refreshSecretProtection()
    await refreshSecretProtection()
    resolveOlder({ ...status, availability: "available" })
    await older
    expect(get(secretProtectionStatus)?.availability).toBe("locked")
    expect(get(secretProtectionLoading)).toBe(false)
  })
  it("retains existing metadata when a refresh fails", async () => {
    secretProtectionStatus.set(status)
    mockInvoke.mockRejectedValue(new Error("failed"))
    await refreshSecretProtection()
    expect(get(secretProtectionStatus)).toEqual(status)
  })
})
