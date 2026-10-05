import { describe, expect, it } from "vitest"
import { mapSecretError } from "./secret-errors.js"

describe("secret errors", () => {
  it.each(["unlock_required", "unlock_failed"])(
    "places %s under the current field",
    (code) => {
      const error = Object.assign(new Error("raw backend text"), {
        cliError: { code, message: "raw backend text" },
      })
      expect(mapSecretError(error).field).toBe("currentPassphrase")
      expect(mapSecretError(error).message).not.toContain("raw backend")
    },
  )
  it.each(["secret_backend_unavailable", "backend_unavailable"])(
    "maps %s",
    (code) => {
      expect(mapSecretError({ cliError: { code } }).recovery).toBe(
        "manage_security",
      )
    },
  )
  it.each(["secret_store_corrupt", "store_corrupt"])(
    "offers recovery for %s",
    (code) => {
      expect(mapSecretError({ cliError: { code } }).recovery).toBe("recovery")
    },
  )
  it("recognizes native cancellation", () => {
    expect(
      mapSecretError({
        ok: false,
        message: "Secret protection change canceled.",
      }).cancelled,
    ).toBe(true)
  })
  it("recognizes explicit unlock cancellation", () => {
    expect(
      mapSecretError({ ok: false, message: "Secret unlock canceled." })
        .cancelled,
    ).toBe(true)
  })
  it("recognizes structured cancellation", () => {
    expect(mapSecretError({ cliError: { code: "canceled" } }).cancelled).toBe(
      true,
    )
  })
  it("does not classify a backend failure as native cancellation", () => {
    expect(
      mapSecretError({
        message: "Secret protection change canceled.",
        cliError: { code: "UNKNOWN" },
      }).cancelled,
    ).toBe(false)
  })
  it("never promotes unknown raw messages", () => {
    expect(
      mapSecretError(new Error("credential sensitive raw value")).message,
    ).toBe("The secret protection change could not be completed.")
    expect(mapSecretError(null).field).toBeUndefined()
  })
})
