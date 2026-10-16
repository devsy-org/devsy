import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/svelte"
import { get } from "svelte/store"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { providers } from "$lib/stores/providers.js"
import type { Provider } from "$lib/types/index.js"

const {
  providerAdd,
  providerInitStreaming,
  providerList,
  providerOptions,
  providerReleaseJob,
  providerSetOptions,
  onCommandProgress,
} = vi.hoisted(() => ({
  providerAdd: vi.fn(),
  providerInitStreaming: vi.fn(),
  providerList: vi.fn(),
  providerOptions: vi.fn(),
  providerReleaseJob: vi.fn(),
  providerSetOptions: vi.fn(),
  onCommandProgress: vi.fn(),
}))

vi.mock("$lib/ipc/commands.js", () => ({
  providerAdd,
  providerInitStreaming,
  providerList,
  providerOptions,
  providerReleaseJob,
  providerSetOptions,
}))

vi.mock("$lib/ipc/events.js", () => ({ onCommandProgress }))
vi.mock("$lib/stores/toasts.js", () => ({
  toasts: { error: vi.fn() },
}))

import ProviderWizard from "./ProviderWizard.svelte"

function makeProvider(name: string): Provider {
  return {
    name,
    version: "0.1.0",
    state: { initialized: true },
  }
}

function renderWizard() {
  return render(ProviderWizard, { open: true })
}

describe("ProviderWizard", () => {
  afterEach(() => cleanup())

  beforeEach(() => {
    vi.clearAllMocks()
    providers.set([])
    providerAdd.mockResolvedValue(undefined)
    providerInitStreaming.mockResolvedValue("init-command")
    providerOptions.mockResolvedValue({})
    providerList.mockResolvedValue([])
    providerReleaseJob.mockResolvedValue(undefined)
    providerSetOptions.mockResolvedValue(undefined)
    onCommandProgress.mockImplementation(async () => () => undefined)
  })

  it("adds a fresh Microsandbox preset with the latest source and keeps it selected", async () => {
    const page = renderWizard()
    const preset = screen.getByRole("button", {
      name: /microsandbox Hardware-isolated microVMs/,
    })

    await fireEvent.click(preset)
    expect(preset.className).toContain("ring-primary")
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    await waitFor(() => {
      expect(providerAdd).toHaveBeenCalledExactlyOnceWith(
        "microsandbox",
        undefined,
        false,
      )
    })
    page.unmount()
  })

  it("shows structured enum labels and saves their values with defaults", async () => {
    providerOptions.mockResolvedValue({
      SIZE: {
        name: "SIZE",
        default: "micro",
        enum: [
          { value: "micro", displayName: "Micro plan" },
          { value: "large", displayName: "Large plan" },
        ],
      },
      MODE: {
        name: "MODE",
        required: true,
        enum: [
          { value: "safe", displayName: "Safe mode" },
          { value: "fast", displayName: "Fast mode" },
        ],
      },
      ENABLED: { name: "ENABLED", type: "boolean", default: false },
    })
    const page = renderWizard()
    await fireEvent.click(
      screen.getByRole("button", { name: /docker Local Docker/ }),
    )
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    expect(await screen.findByText("Micro plan")).toBeDefined()
    const modeTrigger = screen.getByText("Select...").closest("button")
    expect(modeTrigger).not.toBeNull()
    Object.defineProperty(modeTrigger, "hasPointerCapture", {
      value: () => false,
    })
    await fireEvent.pointerDown(modeTrigger as HTMLButtonElement, {
      button: 0,
      pointerType: "mouse",
    })
    await fireEvent.pointerUp(
      await screen.findByRole("option", { name: "Fast mode" }),
      { button: 0, pointerType: "mouse" },
    )
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    await waitFor(() => {
      expect(providerSetOptions).toHaveBeenCalledExactlyOnceWith("docker", {
        SIZE: "micro",
        MODE: "fast",
        ENABLED: "false",
      })
    })
    page.unmount()
  })

  it("saves a selected empty enum value while leaving an unset enum omitted", async () => {
    providerOptions.mockResolvedValue({
      TOKEN: { name: "TOKEN", displayName: "Token", required: true },
      STORED_MODE: {
        name: "STORED_MODE",
        value: "",
        enum: [
          { value: "", displayName: "Stored empty" },
          { value: "fast", displayName: "Fast" },
        ],
      },
      DEFAULT_MODE: {
        name: "DEFAULT_MODE",
        default: "",
        enum: [
          { value: "", displayName: "Default empty" },
          { value: "safe", displayName: "Safe" },
        ],
      },
      UNSET_MODE: {
        name: "UNSET_MODE",
        enum: [
          { value: "", displayName: "No extra settings" },
          { value: "fast", displayName: "Fast" },
        ],
      },
      UNTOUCHED_MODE: {
        name: "UNTOUCHED_MODE",
        enum: [
          { value: "", displayName: "Also unset" },
          { value: "safe", displayName: "Safe" },
        ],
      },
    })
    const page = renderWizard()
    await fireEvent.click(
      screen.getByRole("button", { name: /docker Local Docker/ }),
    )
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    expect(await screen.findByText("Stored empty")).toBeDefined()
    expect(screen.getByText("Default empty")).toBeDefined()
    const modeTrigger = screen.getAllByText("Select...")[0]
    const modeButton = modeTrigger.closest("button")
    expect(modeButton).not.toBeNull()
    Object.defineProperty(modeButton, "hasPointerCapture", {
      value: () => false,
    })
    await fireEvent.pointerDown(modeButton as HTMLButtonElement, {
      button: 0,
      pointerType: "mouse",
    })
    await fireEvent.pointerUp(
      await screen.findByRole("option", { name: "No extra settings" }),
      { button: 0, pointerType: "mouse" },
    )
    await fireEvent.input(screen.getByRole("textbox"), {
      target: { value: "secret" },
    })
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    await waitFor(() => {
      expect(providerSetOptions).toHaveBeenCalledExactlyOnceWith("docker", {
        TOKEN: "secret",
        STORED_MODE: "",
        DEFAULT_MODE: "",
        UNSET_MODE: "",
      })
    })
    page.unmount()
  })

  it("keeps ordinary presets using their name as the source", async () => {
    const page = renderWizard()
    await fireEvent.click(
      screen.getByRole("button", { name: /docker Local Docker/ }),
    )
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    await waitFor(() => {
      expect(providerAdd).toHaveBeenCalledExactlyOnceWith(
        "docker",
        undefined,
        false,
      )
    })
    page.unmount()
  })

  it("forwards the bare Microsandbox alias to the CLI", async () => {
    const page = renderWizard()
    await fireEvent.input(
      screen.getByPlaceholderText("github.com/org/provider"),
      {
        target: { value: "microsandbox" },
      },
    )
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    await waitFor(() => {
      expect(providerAdd).toHaveBeenCalledExactlyOnceWith(
        "microsandbox",
        undefined,
        false,
      )
    })
    page.unmount()
  })

  it("uses a custom name before the preset name", async () => {
    const page = renderWizard()
    await fireEvent.click(
      screen.getByRole("button", {
        name: /microsandbox Hardware-isolated microVMs/,
      }),
    )
    await fireEvent.input(
      screen.getByPlaceholderText("Defaults to source name"),
      {
        target: { value: "micro-lab" },
      },
    )
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    await waitFor(() => {
      expect(providerAdd).toHaveBeenCalledExactlyOnceWith(
        "micro-lab",
        "microsandbox",
        false,
      )
    })
    page.unmount()
  })

  it("does not retain the preset name after the source is edited", async () => {
    const page = renderWizard()
    await fireEvent.click(
      screen.getByRole("button", {
        name: /microsandbox Hardware-isolated microVMs/,
      }),
    )
    await fireEvent.input(
      screen.getByPlaceholderText("github.com/org/provider"),
      {
        target: { value: "https://example.test/provider.yaml" },
      },
    )
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    await waitFor(() => {
      expect(providerAdd).toHaveBeenCalledExactlyOnceWith(
        "https://example.test/provider.yaml",
        undefined,
        false,
      )
    })
    page.unmount()
  })

  it("rejects an existing Microsandbox name before IPC and preserves the provider", async () => {
    const existing = makeProvider("microsandbox")
    providers.set([existing])
    const page = renderWizard()
    await fireEvent.click(
      screen.getByRole("button", {
        name: /microsandbox Hardware-isolated microVMs/,
      }),
    )
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    expect(
      await screen.findByText(
        'Provider "microsandbox" already exists. Choose a different name.',
      ),
    ).toBeTruthy()
    expect(providerAdd).not.toHaveBeenCalled()
    expect(get(providers)).toEqual([existing])
    page.unmount()
  })
})
