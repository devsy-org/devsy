import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/svelte"
import { writable } from "svelte/store"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

const mocks = vi.hoisted(() => ({
  set: vi.fn(),
  attach: vi.fn(),
  refresh: vi.fn(),
  success: vi.fn(),
  info: vi.fn(),
}))
vi.mock("$lib/ipc/commands.js", () => ({
  secretSet: mocks.set,
  secretAttach: mocks.attach,
}))
vi.mock("$lib/stores/secrets.js", () => ({
  secretsError: writable(null),
  secrets: writable([{ name: "EXISTING", context: "staging" }]),
  refreshSecrets: mocks.refresh,
}))
vi.mock("$lib/stores/contexts.js", () => ({
  activeContext: writable("staging"),
}))
vi.mock("$lib/stores/toasts.js", () => ({
  toasts: { success: mocks.success, info: mocks.info },
}))

import AddSecretDialog from "./AddSecretDialog.svelte"
import SecretDetailsSheet from "./SecretDetailsSheet.svelte"

describe("Secret dialogs", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.set.mockResolvedValue(undefined)
    mocks.attach.mockResolvedValue(undefined)
    mocks.refresh.mockResolvedValue(undefined)
  })
  afterEach(cleanup)
  it("rejects duplicate add instead of replacing", async () => {
    render(AddSecretDialog, { open: true })
    await fireEvent.input(screen.getByLabelText("Name"), {
      target: { value: "EXISTING" },
    })
    await fireEvent.input(screen.getByLabelText("Value"), {
      target: { value: "sensitive" },
    })
    expect(
      screen.getByText(/A secret with this name already exists/),
    ).toBeTruthy()
    expect(
      (screen.getByRole("button", { name: "Save" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true)
    expect(mocks.set).not.toHaveBeenCalled()
  })
  it("preserves whitespace and reports saved-but-not-attached without rollback", async () => {
    mocks.attach.mockRejectedValueOnce(new Error("unavailable"))
    render(AddSecretDialog, { open: true })
    await fireEvent.input(screen.getByLabelText("Name"), {
      target: { value: "NEW" },
    })
    await fireEvent.input(screen.getByLabelText("Value"), {
      target: { value: "  value  " },
    })
    await fireEvent.click(screen.getByRole("switch"))
    await fireEvent.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() =>
      expect(mocks.set).toHaveBeenCalledWith("NEW", "  value  ", "staging"),
    )
    expect(mocks.attach).toHaveBeenCalledWith("NEW", "staging")
    await waitFor(() =>
      expect(mocks.info).toHaveBeenCalledWith(
        "Secret saved, but Devsy could not enable workspace injection.",
      ),
    )
  })
  it("clears submitted plaintext after save failure and keeps the dialog open", async () => {
    mocks.set.mockRejectedValueOnce(new Error("could echo sensitive"))
    render(AddSecretDialog, { open: true })
    await fireEvent.input(screen.getByLabelText("Name"), {
      target: { value: "NEW" },
    })
    await fireEvent.input(screen.getByLabelText("Value"), {
      target: { value: "sensitive" },
    })
    await fireEvent.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() =>
      expect(screen.getByRole("alert").textContent).toContain("Unable to save"),
    )
    expect((screen.getByLabelText("Value") as HTMLInputElement).value).toBe("")
    expect(screen.queryByText("could echo sensitive")).toBeNull()
  })
  it("explicit replacement passes the displayed row context", async () => {
    render(SecretDetailsSheet, {
      open: true,
      secret: {
        name: "OLD",
        context: "production",
        backend: "file",
        availability: "locked",
      },
      onAttachmentChange: vi.fn(),
    })
    await fireEvent.click(
      screen.getByRole("button", { name: "Replace secret value" }),
    )
    await fireEvent.input(screen.getByLabelText("New value"), {
      target: { value: "replacement" },
    })
    await fireEvent.click(screen.getByRole("button", { name: "Replace" }))
    await waitFor(() =>
      expect(mocks.set).toHaveBeenCalledWith(
        "OLD",
        "replacement",
        "production",
      ),
    )
    await waitFor(() => expect(screen.queryByLabelText("New value")).toBeNull())
  })
})
