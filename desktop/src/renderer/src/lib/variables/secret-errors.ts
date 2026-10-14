import type { SecretProtectionActionResult } from "$lib/types/index.js"

export interface UserFacingSecretError {
  message: string
  field?: "currentPassphrase" | "newPassphrase"
  recovery?: "retry" | "manage_security" | "recovery"
  cancelled: boolean
}

/** Structured codes determine primary copy; raw backend messages stay out of UI. */
export function mapSecretError(error: unknown): UserFacingSecretError {
  const result =
    error && typeof error === "object"
      ? (error as { message?: unknown; cliError?: { code?: string } })
      : undefined
  const code = result?.cliError?.code
  switch (code) {
    case "unlock_required":
      return {
        message: "Enter your current passphrase to continue.",
        field: "currentPassphrase",
        cancelled: false,
      }
    case "unlock_failed":
      return {
        message: "That passphrase didn't unlock this secret store.",
        field: "currentPassphrase",
        cancelled: false,
      }
    case "secret_backend_unavailable":
    case "backend_unavailable":
      return {
        message:
          "Devsy can't access the key or credential service required by this secret store.",
        recovery: "manage_security",
        cancelled: false,
      }
    case "secret_store_corrupt":
    case "store_corrupt":
      return {
        message: "Devsy couldn't read the encrypted secret store.",
        recovery: "recovery",
        cancelled: false,
      }
    case "secret_not_found":
      return {
        message: "The secret value could not be found.",
        recovery: "manage_security",
        cancelled: false,
      }
    case "canceled":
      return { message: "Secret protection change canceled.", cancelled: true }
  }
  // Native cancellation has no CLI error because the CLI never ran.
  if (
    !code &&
    (result?.message === "Secret protection change canceled." ||
      result?.message === "Secret unlock canceled.")
  ) {
    return { message: "Secret protection change canceled.", cancelled: true }
  }
  return {
    message: "The secret protection change could not be completed.",
    recovery: "retry",
    cancelled: false,
  }
}

export function presentSecretProtectionError(
  result: SecretProtectionActionResult,
): UserFacingSecretError {
  return mapSecretError(result)
}
