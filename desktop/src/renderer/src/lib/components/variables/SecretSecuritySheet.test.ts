import { fireEvent, render, screen, waitFor } from "@testing-library/svelte"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { secretSessionClear, secretUnlockRequest } from "$lib/ipc/commands.js"
import {
  refreshSecretProtection,
  secretProtectionError,
  secretProtectionLoading,
  secretProtectionStatus,
} from "$lib/stores/secret-protection.js"
import type { SecretProtectionStatus } from "$lib/types/index.js"
import SecretSecurityBanner from "./SecretSecurityBanner.svelte"
import SecretSecuritySheet from "./SecretSecuritySheet.svelte"

vi.mock("$lib/ipc/commands.js", () => ({
  secretSessionClear: vi.fn(async () => undefined),
  secretUnlockRequest: vi.fn(async () => ({ ok: true })),
  secretProtectionAction: vi.fn(async () => ({ ok: true })),
}))
vi.mock("$lib/stores/secret-protection.js", async () => {
  const { writable } = await import("svelte/store")
  return {
    secretProtectionStatus: writable(null),
    secretProtectionLoading: writable(false),
    secretProtectionError: writable(null),
    refreshSecretProtection: vi.fn(async () => {}),
  }
})
vi.mock("$lib/stores/secrets.js", () => ({
  refreshSecrets: vi.fn(async () => {}),
}))
vi.mock("$lib/stores/toasts.js", () => ({ toasts: { success: vi.fn() } }))
const available: SecretProtectionStatus = {
  availability: "available",
  keySource: "passphrase",
  remembered: false,
  rememberedAvailable: true,
  sessionUnlocked: true,
  fileEntries: [],
}

describe("Secret Security", () => {
  beforeEach(() => {
    secretProtectionStatus.set(available)
    secretProtectionLoading.set(false)
    secretProtectionError.set(null)
    vi.clearAllMocks()
  })
  it("refreshes protection after clearing session access without treating void as failure", async () => {
    const page = render(SecretSecuritySheet, { open: true })
    await fireEvent.click(
      await screen.findByRole("button", { name: "Clear session passphrase" }),
    )
    await waitFor(() => expect(secretSessionClear).toHaveBeenCalledOnce())
    await waitFor(() =>
      expect(refreshSecretProtection).toHaveBeenCalledTimes(2),
    )
    expect(screen.queryByRole("alert")).toBeNull()
    page.unmount()
  })
  it("disables mutations when a refresh failed and cached status remains", async () => {
    secretProtectionError.set("Unable to load secret security status.")
    const page = render(SecretSecuritySheet, { open: true })
    const change = await screen.findByRole("button", {
      name: "Change passphrase",
    })
    expect((change as HTMLButtonElement).disabled).toBe(true)
    page.unmount()
  })
  it("retains a failed explicit unlock message after reopening the sheet", async () => {
    vi.mocked(secretUnlockRequest).mockResolvedValueOnce({
      ok: false,
      cliError: { code: "unlock_failed", message: "internal" },
    })
    secretProtectionStatus.set({
      ...available,
      availability: "locked",
      sessionUnlocked: false,
    })
    const page = render(SecretSecuritySheet, { open: true })
    await fireEvent.click(await screen.findByRole("button", { name: "Unlock" }))
    await screen.findByText("That passphrase didn't unlock this secret store.")
    page.unmount()
  })
  it("offers truthful session clearing even when device access is remembered", async () => {
    const page = render(SecretSecuritySheet, { open: true })
    await screen.findByRole("button", { name: "Clear session passphrase" })
    secretProtectionStatus.set({ ...available, remembered: true })
    await screen.findByRole("button", { name: "Forget this device" })
    expect(
      screen.getByRole("button", { name: "Clear session passphrase" }),
    ).toBeTruthy()
    expect(screen.queryByLabelText("New passphrase")).toBeNull()
    page.unmount()
  })
  it("retains Forget and session clearing when the encrypted store is unreadable", async () => {
    secretProtectionStatus.set({
      ...available,
      remembered: true,
      availability: "unknown",
      reasonCode: "store_corrupt",
    })
    const page = render(SecretSecuritySheet, { open: true })
    await screen.findByRole("button", { name: "Forget this device" })
    expect(
      screen.getByRole("button", { name: "Clear session passphrase" }),
    ).toBeTruthy()
    expect(
      screen.queryByRole("button", { name: "Change passphrase" }),
    ).toBeNull()
    page.unmount()
  })
  it("keeps the destructive recovery command behind Recovery options", async () => {
    const page = render(SecretSecuritySheet, { open: true })
    await screen.findByRole("button", { name: "Recovery options" })
    expect(
      screen.queryByText("devsy secret protection reset-file-store"),
    ).toBeNull()
    await fireEvent.click(
      screen.getByRole("button", { name: "Recovery options" }),
    )
    await screen.findByText("devsy secret protection reset-file-store")
    expect(
      screen.getByText(/every file-backed secret across all contexts/),
    ).toBeTruthy()
    expect(screen.queryByRole("button", { name: "Reset" })).toBeNull()
    page.unmount()
  })
  it("hides a healthy banner and exposes security management while locked", async () => {
    const onManageSecurity = vi.fn()
    const page = render(SecretSecurityBanner, { onManageSecurity })
    expect(screen.queryByRole("status")).toBeNull()
    secretProtectionStatus.set({
      ...available,
      availability: "locked",
      sessionUnlocked: false,
    })
    await screen.findByText("File-backed secrets are locked")
    await fireEvent.click(
      screen.getByRole("button", { name: "Manage security" }),
    )
    expect(onManageSecurity).toHaveBeenCalledOnce()
    expect(screen.getByRole("button", { name: "Unlock" })).toBeTruthy()
    page.unmount()
  })
})
