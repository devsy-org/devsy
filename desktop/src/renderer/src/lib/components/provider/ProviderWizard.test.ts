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
  providerAvailable,
  providerInitStreaming,
  providerList,
  providerOptions,
  providerReleaseJob,
  providerSetOptions,
  onCommandProgress,
} = vi.hoisted(() => ({
  providerAdd: vi.fn(),
  providerAvailable: vi.fn(),
  providerInitStreaming: vi.fn(),
  providerList: vi.fn(),
  providerOptions: vi.fn(),
  providerReleaseJob: vi.fn(),
  providerSetOptions: vi.fn(),
  onCommandProgress: vi.fn(),
}))

vi.mock("$lib/ipc/commands.js", () => ({
  providerAdd,
  providerAvailable,
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
    providerAvailable.mockResolvedValue([])
    providerInitStreaming.mockResolvedValue("init-command")
    providerOptions.mockResolvedValue({})
    providerList.mockResolvedValue([])
    providerReleaseJob.mockResolvedValue(undefined)
    providerSetOptions.mockResolvedValue(undefined)
    onCommandProgress.mockImplementation(async () => () => undefined)
  })

  it("adds a fresh Microsandbox preset with the latest source and keeps it selected", async () => {
    const page = renderWizard()
    const steps = screen.getByRole("list", { name: "Provider setup steps" })
    expect(
      screen.getByRole("progressbar", { name: "Provider setup progress" }),
    ).toBeDefined()
    expect(steps.querySelector('[aria-current="step"]')?.textContent).toContain(
      "Select",
    )
    const preset = screen.getByRole("button", {
      name: /microsandbox Hardware-isolated microVMs/,
    })

    await fireEvent.click(preset)
    expect(preset.getAttribute("aria-pressed")).toBe("true")
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

  it("shows discovered catalog providers without replacing the preset list", async () => {
    providerAvailable.mockResolvedValue([
      "docker",
      "terraform",
      "terraform",
      " ",
      "civo",
    ])
    const page = renderWizard()
    const terraform = await screen.findByRole("button", { name: /terraform/ })
    expect(terraform.getAttribute("aria-pressed")).toBe("false")
    expect(screen.getAllByRole("button", { name: /terraform/ })).toHaveLength(1)
    expect(
      screen.getByRole("button", { name: /docker Local Docker/ }),
    ).toBeDefined()

    await fireEvent.click(terraform)
    expect(terraform.getAttribute("aria-pressed")).toBe("true")
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))
    await waitFor(() => {
      expect(providerAdd).toHaveBeenCalledExactlyOnceWith(
        "terraform",
        undefined,
        false,
      )
    })
    page.unmount()
  })

  it("keeps custom sources usable when provider discovery fails and can retry", async () => {
    providerAvailable
      .mockRejectedValueOnce(new Error("catalog unavailable"))
      .mockResolvedValueOnce(["terraform"])
    const page = renderWizard()
    expect((await screen.findByRole("alert")).textContent).toContain(
      "catalog unavailable",
    )
    const sourceInput = screen.getByLabelText("Provider source")
    expect((sourceInput as HTMLInputElement).disabled).toBe(false)
    await fireEvent.input(sourceInput, { target: { value: "my-org/provider" } })
    await fireEvent.click(
      screen.getByRole("button", { name: "Retry provider list" }),
    )

    expect(
      await screen.findByRole("button", { name: /terraform/ }),
    ).toBeDefined()
    expect(
      (screen.getByLabelText("Provider source") as HTMLInputElement).value,
    ).toBe("my-org/provider")
    expect(providerAdd).not.toHaveBeenCalled()
    page.unmount()
  })

  it("leaves untouched optional defaults to CLI initialization", async () => {
    providerOptions.mockResolvedValue({
      REGION: { name: "REGION", default: "us-west-2" },
    })
    const page = renderWizard()
    await fireEvent.click(
      screen.getByRole("button", { name: /aws Amazon Web Services/ }),
    )
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    expect(
      await screen.findByRole("heading", { name: "Configure aws" }),
    ).toBeDefined()
    expect(screen.getByLabelText("REGION").getAttribute("placeholder")).toBe(
      "us-west-2",
    )
    expect(providerInitStreaming).not.toHaveBeenCalled()

    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))
    await waitFor(() => {
      expect(providerSetOptions).not.toHaveBeenCalled()
      expect(providerInitStreaming).toHaveBeenCalledExactlyOnceWith("aws")
    })
    page.unmount()
  })

  it("does not submit an untouched expression default", async () => {
    const expression = "${PROVIDER_FOLDER}/config.json"
    providerOptions.mockResolvedValue({
      CONFIG: { name: "CONFIG", default: expression },
    })
    const page = renderWizard()
    await fireEvent.click(
      screen.getByRole("button", { name: /docker Local Docker/ }),
    )
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    const configInput = await screen.findByRole("textbox", { name: "CONFIG" })
    expect((configInput as HTMLInputElement).value).toBe(expression)
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    expect(
      await screen.findByRole("heading", { name: "Initializing docker" }),
    ).toBeDefined()
    expect(providerSetOptions).not.toHaveBeenCalled()
    expect(providerInitStreaming).toHaveBeenCalledExactlyOnceWith("docker")
    page.unmount()
  })

  it("submits an explicitly edited expression default", async () => {
    const expression = "${PROVIDER_FOLDER}/config.json"
    providerOptions.mockResolvedValue({
      CONFIG: { name: "CONFIG", default: expression },
    })
    const page = renderWizard()
    await fireEvent.click(
      screen.getByRole("button", { name: /docker Local Docker/ }),
    )
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    const configInput = (await screen.findByRole("textbox", {
      name: "CONFIG",
    })) as HTMLInputElement
    await fireEvent.input(configInput, {
      target: { value: "/tmp/custom.json" },
    })
    await fireEvent.input(configInput, { target: { value: expression } })
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    await waitFor(() => {
      expect(providerSetOptions).toHaveBeenCalledExactlyOnceWith(
        "docker",
        {
          CONFIG: expression,
        },
        false,
      )
      expect(providerInitStreaming).toHaveBeenCalledExactlyOnceWith("docker")
    })
    page.unmount()
  })

  it.each([true, false])(
    "preserves Reuse machine %s while saving edited options",
    async (reuseMachine) => {
      providerOptions.mockResolvedValue({
        REGION: { name: "REGION", default: "local" },
      })
      const page = renderWizard()
      await fireEvent.click(
        screen.getByRole("button", { name: /docker Local Docker/ }),
      )
      if (reuseMachine)
        await fireEvent.click(
          screen.getByRole("switch", { name: "Reuse machine" }),
        )
      await fireEvent.click(screen.getByRole("button", { name: "Continue" }))
      await fireEvent.input(
        await screen.findByRole("textbox", { name: "REGION" }),
        { target: { value: "remote" } },
      )
      await fireEvent.click(screen.getByRole("button", { name: "Continue" }))
      await waitFor(() => {
        expect(providerSetOptions).toHaveBeenCalledExactlyOnceWith(
          "docker",
          { REGION: "remote" },
          reuseMachine,
        )
        expect(providerInitStreaming).toHaveBeenCalledExactlyOnceWith("docker")
      })
      expect(providerAdd).toHaveBeenCalledExactlyOnceWith(
        "docker",
        undefined,
        reuseMachine,
      )
      page.unmount()
    },
  )

  it("retries loading options without adding the provider a second time", async () => {
    providerOptions
      .mockRejectedValueOnce(new Error("temporary read failure"))
      .mockResolvedValueOnce({ REGION: { name: "REGION", default: "local" } })
    const page = renderWizard()
    await fireEvent.click(
      screen.getByRole("button", { name: /docker Local Docker/ }),
    )
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    expect(
      await screen.findByRole("heading", { name: "Configure docker" }),
    ).toBeDefined()
    expect(
      await screen.findByText(/Failed to load options: temporary read failure/),
    ).toBeDefined()
    expect(
      screen.getByRole("button", { name: "Continue" }).hasAttribute("disabled"),
    ).toBe(true)
    await fireEvent.click(
      screen.getByRole("button", { name: "Retry loading options" }),
    )

    expect(await screen.findByRole("textbox", { name: "REGION" })).toBeDefined()
    expect(providerAdd).toHaveBeenCalledExactlyOnceWith(
      "docker",
      undefined,
      false,
    )
    expect(providerOptions).toHaveBeenCalledTimes(2)
    page.unmount()
  })

  it("continues initialization after a retry loads no visible options", async () => {
    providerOptions
      .mockRejectedValueOnce(new Error("temporary read failure"))
      .mockResolvedValueOnce({})
    const page = renderWizard()
    await fireEvent.click(
      screen.getByRole("button", { name: /docker Local Docker/ }),
    )
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    expect(
      await screen.findByRole("heading", { name: "Configure docker" }),
    ).toBeDefined()
    await fireEvent.click(
      screen.getByRole("button", { name: "Retry loading options" }),
    )
    const continueButton = await screen.findByRole("button", {
      name: "Continue",
    })
    expect(continueButton.hasAttribute("disabled")).toBe(false)

    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))
    expect(
      await screen.findByRole("heading", { name: "Initializing docker" }),
    ).toBeDefined()
    expect(providerAdd).toHaveBeenCalledExactlyOnceWith(
      "docker",
      undefined,
      false,
    )
    expect(providerOptions).toHaveBeenCalledTimes(2)
    expect(providerSetOptions).not.toHaveBeenCalled()
    expect(providerInitStreaming).toHaveBeenCalledExactlyOnceWith("docker")
    page.unmount()
  })

  it("keeps a failed init-start retryable and only reports ready after success", async () => {
    let progressCallback:
      | ((progress: {
          commandId: string
          done: boolean
          success?: boolean
        }) => void)
      | undefined
    onCommandProgress.mockImplementation(async (callback) => {
      progressCallback = callback
      return () => undefined
    })
    providerInitStreaming
      .mockRejectedValueOnce(new Error("worker unavailable"))
      .mockResolvedValueOnce("init-command")
    const page = renderWizard()
    await fireEvent.click(
      screen.getByRole("button", { name: /docker Local Docker/ }),
    )
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    expect(
      await screen.findByRole("heading", { name: "Initializing docker" }),
    ).toBeDefined()
    expect(
      await screen.findByText(
        /Failed to start initialization: worker unavailable/,
      ),
    ).toBeDefined()
    expect(screen.queryByText(/is ready to use/)).toBeNull()
    await fireEvent.click(screen.getByRole("button", { name: "Retry" }))
    expect(providerInitStreaming).toHaveBeenCalledTimes(2)
    expect(screen.queryByText(/is ready to use/)).toBeNull()

    progressCallback?.({ commandId: "init-command", done: true, success: true })
    expect(await screen.findByText("docker is ready to use.")).toBeDefined()
    page.unmount()
  })

  it("labels a skipped init as installed but not initialized", async () => {
    providerInitStreaming.mockRejectedValueOnce(new Error("worker unavailable"))
    const page = renderWizard()
    await fireEvent.click(
      screen.getByRole("button", { name: /docker Local Docker/ }),
    )
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))
    await screen.findByText(
      /Failed to start initialization: worker unavailable/,
    )
    await fireEvent.click(
      screen.getByRole("button", { name: "Skip initialization" }),
    )

    expect(
      await screen.findByText(/was added but is not initialized yet/),
    ).toBeDefined()
    expect(screen.queryByText(/is ready to use/)).toBeNull()
    expect(providerReleaseJob).toHaveBeenCalledExactlyOnceWith("docker")
    page.unmount()
  })

  it("shows enum defaults but submits only explicitly selected options", async () => {
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
    await fireEvent.input(screen.getByRole("textbox", { name: "ENABLED" }), {
      target: { value: "false" },
    })
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    await waitFor(() => {
      expect(providerSetOptions).toHaveBeenCalledExactlyOnceWith(
        "docker",
        {
          MODE: "fast",
          ENABLED: "false",
        },
        false,
      )
    })
    page.unmount()
  })

  it("saves a selected empty enum value while leaving an unset enum omitted", async () => {
    providerOptions.mockResolvedValue({
      TOKEN: { name: "TOKEN", displayName: "Token", required: true },
      STORED_TEXT: { name: "STORED_TEXT", value: "existing" },
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
      HIDDEN_MODE: { name: "HIDDEN_MODE", value: "hidden", hidden: true },
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
    await fireEvent.input(screen.getByRole("textbox", { name: /Token/ }), {
      target: { value: "secret" },
    })
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))

    await waitFor(() => {
      expect(providerSetOptions).toHaveBeenCalledExactlyOnceWith(
        "docker",
        {
          TOKEN: "secret",
          UNSET_MODE: "",
        },
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

    expect(
      await screen.findByText(
        "Enter an installation name for this custom source.",
      ),
    ).toBeDefined()
    expect(providerAdd).not.toHaveBeenCalled()
    await fireEvent.input(
      screen.getByPlaceholderText("Defaults to source name"),
      { target: { value: "custom-provider" } },
    )
    await fireEvent.click(screen.getByRole("button", { name: "Continue" }))
    await waitFor(() => {
      expect(providerAdd).toHaveBeenCalledExactlyOnceWith(
        "custom-provider",
        "https://example.test/provider.yaml",
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
