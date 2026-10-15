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

const microsandboxSource =
  "github.com/devsy-org/devsy-provider-microsandbox@v0.1.8"

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

  it("adds a fresh Microsandbox preset with the pinned source and keeps it selected", async () => {
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
        microsandboxSource,
        false,
      )
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
        microsandboxSource,
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
