import { describe, expect, it } from "vitest"
import type { SecretProtectionStatus } from "$lib/types/index.js"
import { variableIdentity } from "./identity.js"
import {
  getSecretProtectionViewState,
  secretProtectionModeLabel,
  secretProtectionStateLabel,
} from "./secret-protection-state.js"

const base: SecretProtectionStatus = {
  keySource: "passphrase",
  availability: "available",
  remembered: false,
  rememberedAvailable: true,
  fileEntries: [],
  sessionUnlocked: false,
}

describe("protection presentation state", () => {
  it.each([
    [
      {
        keySource: "",
        availability: "unknown",
        reasonCode: "file_store_uninitialized",
      },
      "uninitialized",
    ],
    [{ keySource: "file" }, "automatic_available"],
    [{ keySource: "keyring" }, "automatic_available"],
    [{ availability: "locked" }, "passphrase_locked"],
    [{ sessionUnlocked: true }, "passphrase_available"],
    [{ remembered: true }, "passphrase_available"],
    [
      { rememberedAvailable: false, availability: "backend_unavailable" },
      "backend_unavailable",
    ],
    [
      { availability: "missing", reasonCode: "file_store_missing" },
      "file_store_missing",
    ],
    [{ availability: "unknown", reasonCode: "store_corrupt" }, "store_corrupt"],
    [{ reasonCode: "ownership_unknown" }, "ownership_unknown"],
    [{ keySource: "future" }, "unknown"],
    [{ availability: "unknown" }, "unknown"],
  ])("maps %j to %s", (override, expected) => {
    expect(
      getSecretProtectionViewState({
        ...base,
        ...override,
      } as SecretProtectionStatus),
    ).toBe(expected)
  })
  it("does not confuse an inaccessible remembered service with unavailable values", () => {
    expect(
      getSecretProtectionViewState({
        ...base,
        rememberedAvailable: false,
        sessionUnlocked: true,
      }),
    ).toBe("passphrase_available")
  })
  it("does not infer value access from stale session or remembered flags", () => {
    expect(
      getSecretProtectionViewState({
        ...base,
        availability: "locked",
        remembered: true,
        sessionUnlocked: true,
      }),
    ).toBe("passphrase_locked")
  })
  it("labels automatic modes using the exact Go source", () => {
    expect(secretProtectionModeLabel({ ...base, keySource: "keyring" })).toBe(
      "Automatic key",
    )
    expect(secretProtectionModeLabel({ ...base, keySource: "file" })).toBe(
      "Automatic key",
    )
    expect(secretProtectionModeLabel(null)).toBe("Unknown")
    expect(secretProtectionStateLabel("passphrase_locked")).toBe("Locked")
  })
  it("handles unloaded status", () =>
    expect(getSecretProtectionViewState(null)).toBe("unknown"))
  it("uses both row dimensions without separator collisions", () => {
    expect(variableIdentity("a:b", "c")).not.toBe(variableIdentity("a", "b:c"))
    expect(variableIdentity("a", "TOKEN")).not.toBe(
      variableIdentity("b", "TOKEN"),
    )
  })
})
