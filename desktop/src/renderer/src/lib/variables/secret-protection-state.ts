import type { SecretProtectionStatus } from "$lib/types/index.js"

export type SecretProtectionViewState =
  | "uninitialized"
  | "automatic_available"
  | "passphrase_available"
  | "passphrase_locked"
  | "file_store_missing"
  | "backend_unavailable"
  | "store_corrupt"
  | "ownership_unknown"
  | "unknown"

export function getSecretProtectionViewState(
  status: SecretProtectionStatus | null,
): SecretProtectionViewState {
  if (!status) return "unknown"
  switch (status.reasonCode) {
    case "ownership_unknown":
      return "ownership_unknown"
    case "store_corrupt":
      return "store_corrupt"
    case "file_store_missing":
      return "file_store_missing"
    case "file_store_uninitialized":
      return "uninitialized"
  }
  if (status.availability === "missing") return "file_store_missing"
  if (status.availability === "backend_unavailable")
    return "backend_unavailable"
  if (status.keySource === "passphrase") {
    if (status.availability === "locked") return "passphrase_locked"
    if (status.availability === "available") return "passphrase_available"
  }
  if (
    (status.keySource === "keyring" || status.keySource === "file") &&
    status.availability === "available"
  )
    return "automatic_available"
  return "unknown"
}

export function secretProtectionStateLabel(
  state: SecretProtectionViewState,
): string {
  switch (state) {
    case "uninitialized":
      return "Not initialized"
    case "automatic_available":
    case "passphrase_available":
      return "Available"
    case "passphrase_locked":
      return "Locked"
    case "file_store_missing":
      return "Missing secret store"
    case "backend_unavailable":
      return "Unavailable"
    case "store_corrupt":
      return "Unreadable secret store"
    case "ownership_unknown":
      return "Needs attention"
    case "unknown":
      return "Unknown"
  }
}

export function secretProtectionModeLabel(
  status: SecretProtectionStatus | null,
): string {
  switch (status?.keySource) {
    case "passphrase":
      return "Passphrase"
    case "keyring":
    case "file":
      return "Automatic key"
    case "":
      return "Not initialized"
    default:
      return "Unknown"
  }
}
