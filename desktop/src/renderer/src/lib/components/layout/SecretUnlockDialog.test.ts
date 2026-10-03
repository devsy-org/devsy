import { fireEvent, render, screen, waitFor } from "@testing-library/svelte"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { invoke, listen } from "$lib/ipc/bridge.js"
import SecretUnlockDialog from "./SecretUnlockDialog.svelte"

vi.mock("$lib/stores/secrets.js", () => ({
  refreshSecrets: vi.fn(async () => undefined),
}))
vi.mock("$lib/ipc/bridge.js", () => ({ invoke: vi.fn(), listen: vi.fn() }))

describe("SecretUnlockDialog", () => {
  let request: () => void
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(listen).mockImplementation(async (_name, callback) => {
      request = () => callback({ payload: {} })
      return vi.fn<() => void>()
    })
    vi.mocked(invoke).mockResolvedValue({ ok: true })
  })
  it("opens on a main-process unlock request and submits only with explicit remember selection", async () => {
    const { unmount } = render(SecretUnlockDialog)
    await waitFor(() => expect(listen).toHaveBeenCalled())
    request()
    const input = await screen.findByLabelText("Secrets passphrase")
    await fireEvent.input(input, { target: { value: "private-credential" } })
    await fireEvent.click(screen.getByText("Unlock and retry"))
    await waitFor(() =>
      expect(invoke).toHaveBeenCalledWith("secret_unlock_submit", {
        passphrase: "private-credential",
        remember: false,
      }),
    )
    await waitFor(() =>
      expect(screen.queryByLabelText("Secrets passphrase")).toBeNull(),
    )
    unmount()
  })
  it("cancels without submitting credential material", async () => {
    const { unmount } = render(SecretUnlockDialog)
    await waitFor(() => expect(listen).toHaveBeenCalled())
    request()
    await screen.findByLabelText("Secrets passphrase")
    await fireEvent.click(screen.getByText("Cancel"))
    await waitFor(() =>
      expect(invoke).toHaveBeenCalledWith("secret_unlock_submit", {}),
    )
    unmount()
  })
})
