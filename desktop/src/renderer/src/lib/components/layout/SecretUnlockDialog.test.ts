import { fireEvent, render, screen, waitFor } from "@testing-library/svelte"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { invoke, listen } from "$lib/ipc/bridge.js"
import SecretUnlockDialog from "./SecretUnlockDialog.svelte"

vi.mock("$lib/stores/secrets.js", () => ({
  refreshSecrets: vi.fn(async () => undefined),
}))
vi.mock("$lib/ipc/bridge.js", () => ({ invoke: vi.fn(), listen: vi.fn() }))

describe("SecretUnlockDialog", () => {
  let request: (requestId?: unknown) => void
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(listen).mockImplementation(async (_name, callback) => {
      request = (requestId) => callback({ payload: { requestId } })
      return vi.fn<() => void>()
    })
    vi.mocked(invoke).mockResolvedValue({ ok: true })
  })
  it("opens on a main-process unlock request and submits only with explicit remember selection", async () => {
    const { unmount } = render(SecretUnlockDialog)
    await waitFor(() => expect(listen).toHaveBeenCalled())
    request("request-1")
    const input = await screen.findByLabelText("Secrets passphrase")
    await fireEvent.input(input, { target: { value: "private-credential" } })
    await fireEvent.click(screen.getByText("Unlock and retry"))
    await waitFor(() =>
      expect(invoke).toHaveBeenCalledWith("secret_unlock_submit", {
        requestId: "request-1",
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
    request("request-2")
    await screen.findByLabelText("Secrets passphrase")
    await fireEvent.click(screen.getByText("Cancel"))
    await waitFor(() =>
      expect(invoke).toHaveBeenCalledWith("secret_unlock_submit", {
        requestId: "request-2",
      }),
    )
    unmount()
  })

  it("ignores malformed unlock notifications", async () => {
    const { unmount } = render(SecretUnlockDialog)
    await waitFor(() => expect(listen).toHaveBeenCalled())
    request(undefined)
    request("")
    request("   ")
    request(123)
    expect(screen.queryByLabelText("Secrets passphrase")).toBeNull()
    unmount()
  })

  it("keeps a newer request intact when an older submit resolves later", async () => {
    let resolveOld!: (value: { ok: boolean }) => void
    vi.mocked(invoke).mockImplementationOnce(
      () => new Promise((resolve) => (resolveOld = resolve)),
    )
    const { unmount } = render(SecretUnlockDialog)
    await waitFor(() => expect(listen).toHaveBeenCalled())

    request("request-old")
    const input = await screen.findByLabelText("Secrets passphrase")
    await fireEvent.input(input, { target: { value: "old credential" } })
    await fireEvent.click(screen.getByText("Unlock and retry"))
    await waitFor(() =>
      expect(invoke).toHaveBeenCalledWith("secret_unlock_submit", {
        requestId: "request-old",
        passphrase: "old credential",
        remember: false,
      }),
    )

    request("request-new")
    await fireEvent.input(input, { target: { value: "new credential" } })
    resolveOld({ ok: true })

    await waitFor(() =>
      expect(
        (screen.getByLabelText("Secrets passphrase") as HTMLInputElement).value,
      ).toBe("new credential"),
    )
    expect(
      (screen.getByLabelText("Secrets passphrase") as HTMLInputElement)
        .disabled,
    ).toBe(false)
    unmount()
  })

  it("does not reset typing or busy state when the same request is re-notified", async () => {
    vi.mocked(invoke).mockImplementationOnce(() => new Promise(() => {}))
    const { unmount } = render(SecretUnlockDialog)
    await waitFor(() => expect(listen).toHaveBeenCalled())
    request("request-same")
    const input = await screen.findByLabelText("Secrets passphrase")
    await fireEvent.input(input, { target: { value: "typed value" } })
    await fireEvent.click(screen.getByText("Unlock and retry"))
    request("request-same")
    expect((input as HTMLInputElement).value).toBe("typed value")
    expect((input as HTMLInputElement).disabled).toBe(true)
    unmount()
  })
})
