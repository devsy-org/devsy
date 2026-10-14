import type { Secret } from "$lib/types/index.js"

export function secretStorage(secret: Secret): string {
  return secret.backend === "keyring"
    ? "OS keychain"
    : secret.backend === "file"
      ? "Encrypted file"
      : "Unknown"
}

export function secretAvailability(secret: Secret): string {
  switch (secret.availability ?? (secret.orphaned ? "missing" : "unknown")) {
    case "available":
      return "Available"
    case "locked":
      return "Locked"
    case "missing":
      return "Missing value"
    case "backend_unavailable":
      return "Unavailable"
    default:
      return "Unknown"
  }
}
