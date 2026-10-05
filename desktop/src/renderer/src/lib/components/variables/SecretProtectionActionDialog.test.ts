import { fireEvent, render, screen, waitFor } from "@testing-library/svelte"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { secretProtectionAction } from "$lib/ipc/commands.js"
import { refreshSecretProtection } from "$lib/stores/secret-protection.js"
import { refreshSecrets } from "$lib/stores/secrets.js"
import type { SecretProtectionStatus } from "$lib/types/index.js"
import SecretProtectionActionDialog from "./SecretProtectionActionDialog.svelte"

vi.mock("$lib/ipc/commands.js", () => ({ secretProtectionAction: vi.fn() }))
vi.mock("$lib/stores/secret-protection.js", () => ({
  refreshSecretProtection: vi.fn(async () => {}),
}))
vi.mock("$lib/stores/secrets.js", () => ({
  refreshSecrets: vi.fn(async () => {}),
}))
vi.mock("$lib/stores/toasts.js", () => ({ toasts: { success: vi.fn() } }))

const locked: SecretProtectionStatus = {
  availability: "locked",
  keySource: "passphrase",
  remembered: false,
  rememberedAvailable: true,
  sessionUnlocked: false,
  fileEntries: [],
}
describe("SecretProtectionActionDialog", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(secretProtectionAction).mockResolvedValue({ ok: true })
  })
  it("keeps failed removal inline, clears submitted credentials, and permits retry", async () => {
    vi.mocked(secretProtectionAction).mockResolvedValueOnce({
      ok: false,
      cliError: { code: "unlock_failed", message: "internal details" },
    })
    const page = render(SecretProtectionActionDialog, {
      open: true,
      action: "remove-passphrase",
      status: locked,
    })
    const input = await screen.findByLabelText("Current passphrase")
    await fireEvent.input(input, { target: { value: "incorrect" } })
    await fireEvent.click(
      screen.getByRole("button", { name: "Remove protection" }),
    )
    await screen.findByText("That passphrase didn't unlock this secret store.")
    expect((input as HTMLInputElement).value).toBe("")
    expect(secretProtectionAction).toHaveBeenCalledWith({
      action: "remove-passphrase",
      currentPassphrase: "incorrect",
    })
    expect(refreshSecrets).not.toHaveBeenCalled()
    await fireEvent.input(input, { target: { value: "correct" } })
    await fireEvent.click(
      screen.getByRole("button", { name: "Remove protection" }),
    )
    await waitFor(() => expect(refreshSecrets).toHaveBeenCalledOnce())
    expect(refreshSecretProtection).toHaveBeenCalledOnce()
    await waitFor(() =>
      expect(screen.queryByLabelText("Current passphrase")).toBeNull(),
    )
    page.unmount()
  })
  it("validates confirmation before IPC and permits short matching passphrases", async () => {
    const page = render(SecretProtectionActionDialog, {
      open: true,
      action: "set-passphrase",
      status: locked,
    })
    await fireEvent.input(await screen.findByLabelText("New passphrase"), {
      target: { value: "short" },
    })
    await fireEvent.click(
      screen.getByRole("button", { name: "Use a passphrase" }),
    )
    await screen.findByText("Passphrases do not match.")
    expect(secretProtectionAction).not.toHaveBeenCalled()
    await fireEvent.input(screen.getByLabelText("Confirm new passphrase"), {
      target: { value: "short" },
    })
    await fireEvent.click(
      screen.getByRole("button", { name: "Use a passphrase" }),
    )
    await waitFor(() =>
      expect(secretProtectionAction).toHaveBeenCalledWith({
        action: "set-passphrase",
        newPassphrase: "short",
      }),
    )
    page.unmount()
  })
  it("omits current input only when availability is verified", async () => {
    const page = render(SecretProtectionActionDialog, {
      open: true,
      action: "change-passphrase",
      status: { ...locked, availability: "available" },
    })
    await screen.findByLabelText("New passphrase")
    expect(screen.queryByLabelText("Current passphrase")).toBeNull()
    page.unmount()
    const cached = render(SecretProtectionActionDialog, {
      open: true,
      action: "change-passphrase",
      status: { ...locked, sessionUnlocked: true, remembered: true },
    })
    await screen.findByLabelText("Current passphrase")
    cached.unmount()
  })
  it("handles native cancellation without an error or false success", async () => {
    vi.mocked(secretProtectionAction).mockResolvedValue({
      ok: false,
      message: "Secret protection change canceled.",
    })
    const page = render(SecretProtectionActionDialog, {
      open: true,
      action: "forget",
      status: locked,
    })
    await fireEvent.click(
      await screen.findByRole("button", { name: "Forget this device" }),
    )
    await waitFor(() => expect(secretProtectionAction).toHaveBeenCalled())
    expect(screen.queryByRole("alert")).toBeNull()
    expect(refreshSecrets).not.toHaveBeenCalled()
    page.unmount()
  })
})
