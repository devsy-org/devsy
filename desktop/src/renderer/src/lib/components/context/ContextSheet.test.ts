import { cleanup, fireEvent, render } from "@testing-library/svelte"
import { tick } from "svelte"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { contextOptions, contextSetOptions } from "$lib/ipc/commands.js"
import { toasts } from "$lib/stores/toasts.js"
import ContextSheet from "./ContextSheet.svelte"

vi.mock("$lib/ipc/commands.js", async (importOriginal) => ({
  ...(await importOriginal<typeof import("$lib/ipc/commands.js")>()),
  contextOptions: vi.fn(),
  contextSetOptions: vi.fn(),
  contextDelete: vi.fn(),
}))

vi.mock("$lib/stores/contexts.js", () => ({ refreshContexts: vi.fn() }))
vi.mock("$lib/stores/toasts.js", () => ({
  toasts: { success: vi.fn(), error: vi.fn() },
}))

type RawOptions = Awaited<ReturnType<typeof contextOptions>>
let responses: Array<(options: RawOptions) => void>
let rejections: Array<(reason: unknown) => void>

async function settle() {
  for (let i = 0; i < 5; i++) await tick()
}

beforeEach(() => {
  responses = []
  rejections = []
  vi.mocked(contextSetOptions).mockReset()
  vi.mocked(toasts.success).mockClear()
  vi.mocked(toasts.error).mockClear()
  vi.mocked(contextOptions).mockReset()
  vi.mocked(contextOptions).mockImplementation(
    () =>
      new Promise((resolve, reject) => {
        responses.push(resolve)
        rejections.push(reject)
      }),
  )
})
afterEach(cleanup)

describe("ContextSheet option loading", () => {
  it("preserves an initial ready edit when delayed initial loads finish", async () => {
    const ui = render(ContextSheet, {
      props: {
        context: { name: "default" },
        isActive: true,
        open: true,
      },
    })
    await settle()
    expect(responses).toHaveLength(1)
    responses[0]({})
    await settle()
    const github = ui.getByRole("switch", { name: "GitHub CLI Credentials" })
    expect(github.hasAttribute("disabled")).toBe(false)
    await fireEvent.click(github)
    expect(ui.getByRole("button", { name: "Save" })).toBeTruthy()

    for (const resolve of responses.slice(1)) resolve({})
    await settle()
    expect({
      checked: github.getAttribute("aria-checked"),
      saveAvailable: !!ui.queryByRole("button", { name: "Save" }),
      dirty: !!ui.queryByText("Unsaved changes"),
    }).toEqual({ checked: "true", saveAvailable: true, dirty: true })
  })

  it("preserves edits when the context list replaces the same named context", async () => {
    const ui = render(ContextSheet, {
      props: {
        context: { name: "default" },
        isActive: true,
        open: true,
      },
    })
    await settle()
    responses[0]({})
    await settle()
    for (const resolve of responses.slice(1)) resolve({})
    await settle()
    const initialRequests = responses.length
    const github = ui.getByRole("switch", { name: "GitHub CLI Credentials" })
    await fireEvent.click(github)
    expect(ui.getByRole("button", { name: "Save" })).toBeTruthy()

    await ui.rerender({ context: { name: "default" } })
    await settle()
    for (const resolve of responses.slice(initialRequests)) resolve({})
    await settle()
    expect({
      checked: github.getAttribute("aria-checked"),
      saveAvailable: !!ui.queryByRole("button", { name: "Save" }),
      dirty: !!ui.queryByText("Unsaved changes"),
    }).toEqual({ checked: "true", saveAvailable: true, dirty: true })
  })

  it("ignores an old context response after switching contexts", async () => {
    const ui = render(ContextSheet, {
      props: { context: { name: "default" }, isActive: true, open: true },
    })
    await settle()
    await ui.rerender({ context: { name: "staging" } })
    await settle()
    expect(vi.mocked(contextOptions).mock.calls.map(([name]) => name)).toEqual([
      "default",
      "staging",
    ])
    responses[1]({ SSH_INJECT_GITHUB_CREDENTIALS: { value: "true" } })
    await settle()
    const github = ui.getByRole("switch", { name: "GitHub CLI Credentials" })
    expect(github.getAttribute("aria-checked")).toBe("true")
    responses[0]({})
    await settle()
    expect(github.getAttribute("aria-checked")).toBe("true")
    expect(ui.queryByRole("button", { name: "Save" })).toBeNull()
  })

  it("ignores a closed request and explicitly refreshes on reopening", async () => {
    const ui = render(ContextSheet, {
      props: { context: { name: "default" }, isActive: true, open: true },
    })
    await settle()
    await ui.rerender({ open: false })
    await settle()
    await ui.rerender({ open: true })
    await settle()
    expect(responses).toHaveLength(2)
    responses[0]({ SSH_INJECT_GITHUB_CREDENTIALS: { value: "true" } })
    await settle()
    expect(
      ui.queryByRole("switch", { name: "GitHub CLI Credentials" }),
    ).toBeNull()
    responses[1]({})
    await settle()
    expect(
      ui
        .getByRole("switch", { name: "GitHub CLI Credentials" })
        .getAttribute("aria-checked"),
    ).toBe("false")
    expect(contextOptions).toHaveBeenCalledTimes(2)
  })

  it("retains edits after failed save and saves only the changed option on retry", async () => {
    vi.mocked(contextSetOptions)
      .mockRejectedValueOnce(new Error("save unavailable"))
      .mockResolvedValue(undefined)
    const ui = render(ContextSheet, {
      props: { context: { name: "default" }, isActive: true, open: true },
    })
    await settle()
    responses[0]({})
    await settle()
    const github = ui.getByRole("switch", { name: "GitHub CLI Credentials" })
    await fireEvent.click(github)
    await fireEvent.click(ui.getByRole("button", { name: "Save" }))
    await settle()
    expect(github.getAttribute("aria-checked")).toBe("true")
    expect(ui.getByText("Unsaved changes")).toBeTruthy()
    expect(toasts.error).toHaveBeenCalledWith(
      "Failed to save: save unavailable",
    )
    await fireEvent.click(ui.getByRole("button", { name: "Save" }))
    await settle()
    expect(contextSetOptions).toHaveBeenNthCalledWith(
      2,
      ["SSH_INJECT_GITHUB_CREDENTIALS=true"],
      "default",
    )
    expect(ui.queryByText("Unsaved changes")).toBeNull()
    expect(toasts.success).toHaveBeenCalledWith("Options saved")
    expect(contextOptions).toHaveBeenCalledTimes(1)
    await ui.rerender({ open: false })
    await settle()
    await ui.rerender({ open: true })
    await settle()
    responses[1]({ SSH_INJECT_GITHUB_CREDENTIALS: { value: "true" } })
    await settle()
    expect(
      ui
        .getByRole("switch", { name: "GitHub CLI Credentials" })
        .getAttribute("aria-checked"),
    ).toBe("true")
    expect(ui.queryByRole("button", { name: "Save" })).toBeNull()
  })

  it("keeps defaults available when the initial request fails", async () => {
    vi.mocked(contextOptions).mockRejectedValueOnce(
      Object.assign(new Error("raw transport detail"), {
        cliError: {
          code: "context_load_failed",
          message: "options unavailable",
        },
      }),
    )
    const ui = render(ContextSheet, {
      props: { context: { name: "default" }, isActive: true, open: true },
    })
    await settle()
    const github = ui.getByRole("switch", { name: "GitHub CLI Credentials" })
    expect(github.getAttribute("aria-checked")).toBe("false")
    expect(github.hasAttribute("disabled")).toBe(false)
    expect(ui.queryByRole("button", { name: "Save" })).toBeNull()
    expect(contextOptions).toHaveBeenCalledTimes(1)
    expect(toasts.error).toHaveBeenCalledExactlyOnceWith(
      "Failed to load context options: options unavailable",
    )
    expect(contextSetOptions).not.toHaveBeenCalled()
  })
  it("uses defaults after a different context fails to load without saving previous context values", async () => {
    const ui = render(ContextSheet, {
      props: { context: { name: "default" }, isActive: true, open: true },
    })
    await settle()
    responses[0]({
      SSH_INJECT_GITHUB_CREDENTIALS: { value: "true" },
      AGENT_URL: { value: "https://a.invalid" },
    })
    await settle()
    vi.mocked(contextOptions).mockRejectedValueOnce(
      new Error("staging unavailable"),
    )
    await ui.rerender({ context: { name: "staging" } })
    await settle()
    const github = ui.getByRole("switch", { name: "GitHub CLI Credentials" })
    expect(github.getAttribute("aria-checked")).toBe("false")
    expect(ui.queryByDisplayValue("https://a.invalid")).toBeNull()
    expect(ui.queryByRole("button", { name: "Save" })).toBeNull()
    await fireEvent.click(github)
    await fireEvent.click(ui.getByRole("button", { name: "Save" }))
    await settle()
    expect(contextSetOptions).toHaveBeenCalledWith(
      ["SSH_INJECT_GITHUB_CREDENTIALS=true"],
      "staging",
    )
  })
  it.each(["close", "context change"])(
    "ignores a rejected request after %s",
    async (boundary) => {
      const ui = render(ContextSheet, {
        props: { context: { name: "default" }, isActive: true, open: true },
      })
      await settle()
      if (boundary === "close") await ui.rerender({ open: false })
      else await ui.rerender({ context: { name: "staging" } })
      await settle()
      rejections[0](new Error("obsolete request failed"))
      await settle()
      expect(toasts.error).not.toHaveBeenCalled()
      if (boundary === "close") {
        await ui.rerender({ open: true })
        await settle()
      }
      expect(
        ui.queryByRole("switch", { name: "GitHub CLI Credentials" }),
      ).toBeNull()
      responses[1]({ SSH_INJECT_GITHUB_CREDENTIALS: { value: "true" } })
      await settle()
      expect(
        ui
          .getByRole("switch", { name: "GitHub CLI Credentials" })
          .getAttribute("aria-checked"),
      ).toBe("true")
      expect(ui.queryByRole("button", { name: "Save" })).toBeNull()
      expect(toasts.error).not.toHaveBeenCalled()
    },
  )
})
