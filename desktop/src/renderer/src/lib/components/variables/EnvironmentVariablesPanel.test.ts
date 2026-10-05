import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/svelte"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

const mocks = vi.hoisted(() => ({
  envAttach: vi.fn().mockResolvedValue(undefined),
  envDetach: vi.fn().mockResolvedValue(undefined),
  envDelete: vi.fn().mockResolvedValue(undefined),
  refreshEnv: vi.fn().mockResolvedValue(undefined),
  envSet: vi.fn().mockResolvedValue(undefined),
  envList: vi.fn().mockResolvedValue([]),
}))
vi.mock("$lib/ipc/commands.js", () => ({
  envAttach: mocks.envAttach,
  envDelete: mocks.envDelete,
  envDetach: mocks.envDetach,
  envSet: mocks.envSet,
  envList: mocks.envList,
}))
vi.mock("$lib/stores/env.js", async () => {
  const { writable } = await import("svelte/store")
  return {
    envLoading: writable(false),
    envError: writable(null),
    initEnv: mocks.refreshEnv,
    envVars: writable([
      { name: "ATTACHED", value: "one", context: "default", attached: true },
      { name: "DETACHED", value: "two", context: "default", attached: false },
      {
        name: "STAGING_ONLY",
        value: "three",
        context: "staging",
        attached: false,
      },
    ]),
    refreshEnv: mocks.refreshEnv,
  }
})

vi.mock("$lib/stores/contexts.js", async () => ({
  activeContext: (await import("svelte/store")).writable("default"),
}))

import { activeContext } from "$lib/stores/contexts.js"
import { envError, envVars } from "$lib/stores/env.js"
import AddDialog from "./AddEnvironmentVariableDialog.svelte"
import EnvPage from "./EnvironmentVariablesPanel.svelte"

describe("EnvPage managed environment attachments", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    activeContext.set("default")
    envError.set(null)
    envVars.set([
      { name: "ATTACHED", value: "one", context: "default", attached: true },
      { name: "DETACHED", value: "two", context: "default", attached: false },
      {
        name: "STAGING_ONLY",
        value: "three",
        context: "staging",
        attached: false,
      },
    ])
  })
  afterEach(() => {
    cleanup()
  })

  it("renders attachment state and sends attach/detach intent", async () => {
    render(EnvPage)

    expect(
      screen
        .getByRole("switch", { name: "Inject ATTACHED into workspaces" })
        .getAttribute("aria-checked"),
    ).toBe("true")
    const detached = screen.getByRole("switch", {
      name: "Inject DETACHED into workspaces",
    })
    expect(detached.getAttribute("aria-checked")).toBe("false")

    await fireEvent.click(detached)
    await waitFor(() =>
      expect(mocks.envAttach).toHaveBeenCalledWith("DETACHED", "default"),
    )
    expect(mocks.refreshEnv).toHaveBeenCalled()

    await fireEvent.click(
      screen.getByRole("switch", { name: "Inject ATTACHED into workspaces" }),
    )
    await waitFor(() =>
      expect(mocks.envDetach).toHaveBeenCalledWith("ATTACHED", "default"),
    )
  })

  it("uses the context displayed on a stale row", async () => {
    render(EnvPage)

    await fireEvent.click(
      screen.getByRole("switch", {
        name: "Inject STAGING_ONLY into workspaces",
      }),
    )
    await waitFor(() =>
      expect(mocks.envAttach).toHaveBeenCalledWith("STAGING_ONLY", "staging"),
    )
  })

  it("deletes using the context displayed on a stale row", async () => {
    render(EnvPage)

    await fireEvent.click(screen.getByRole("button", { name: "STAGING_ONLY" }))
    await fireEvent.click(
      screen.getByRole("button", { name: "Delete variable" }),
    )
    await fireEvent.click(screen.getByRole("button", { name: /^Delete$/ }))

    await waitFor(() =>
      expect(mocks.envDelete).toHaveBeenCalledWith("STAGING_ONLY", "staging"),
    )
  })
})

describe("environment table value workflows", () => {
  afterEach(cleanup)
  beforeEach(() => {
    vi.clearAllMocks()
    activeContext.set("default")
    envError.set(null)
    envVars.set([
      {
        name: "SHARED",
        value: "default-value",
        context: "default",
        attached: false,
      },
      {
        name: "SHARED",
        value: "staging-value",
        context: "staging",
        attached: false,
      },
    ])
  })
  it("reveals only the exact context and name", async () => {
    render(EnvPage)
    expect(screen.queryByText("default-value")).toBeNull()
    await fireEvent.click(
      screen.getAllByRole("button", { name: "Show value for SHARED" })[1],
    )
    expect(screen.getByText("staging-value")).toBeTruthy()
    expect(screen.queryByText("default-value")).toBeNull()
  })
  it("shows load error and retry", async () => {
    envError.set("offline")
    render(EnvPage)
    await fireEvent.click(screen.getByRole("button", { name: "Retry" }))
    expect(mocks.refreshEnv).toHaveBeenCalled()
  })
  it("updates the selected row context with an empty value", async () => {
    render(EnvPage)
    await fireEvent.click(screen.getAllByRole("button", { name: "SHARED" })[1])
    await fireEvent.input(screen.getByLabelText("Value"), {
      target: { value: "" },
    })
    await fireEvent.click(screen.getByRole("button", { name: "Update value" }))
    await waitFor(() =>
      expect(mocks.envSet).toHaveBeenCalledWith("SHARED", "", "staging"),
    )
  })
  it("rejects duplicate Add instead of silently updating", async () => {
    render(AddDialog, { open: true })
    await fireEvent.input(screen.getByLabelText("Name"), {
      target: { value: "SHARED" },
    })
    expect(screen.getByRole("alert").textContent).toContain("already exists")
    expect(
      screen.getByRole("button", { name: "Save" }).hasAttribute("disabled"),
    ).toBe(true)
    expect(mocks.envSet).not.toHaveBeenCalled()
  })
  it("rejects a duplicate discovered in the fresh list without overwriting", async () => {
    mocks.envList.mockResolvedValueOnce([
      { name: "NEW", value: "existing", context: "default" },
    ])
    render(AddDialog, { open: true })
    await fireEvent.input(screen.getByLabelText("Name"), {
      target: { value: "NEW" },
    })
    await fireEvent.input(screen.getByLabelText("Value"), {
      target: { value: "replacement" },
    })
    await fireEvent.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() =>
      expect(screen.getByRole("alert").textContent).toContain("already exists"),
    )
    expect(mocks.envList).toHaveBeenCalledOnce()
    expect(mocks.refreshEnv).toHaveBeenCalledOnce()
    expect(mocks.envSet).not.toHaveBeenCalled()
    expect(mocks.envAttach).not.toHaveBeenCalled()
  })
  it("fails closed when a fresh duplicate check is unavailable", async () => {
    mocks.envList.mockRejectedValueOnce(new Error("offline"))
    render(AddDialog, { open: true })
    await fireEvent.input(screen.getByLabelText("Name"), {
      target: { value: "NEW" },
    })
    await fireEvent.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() =>
      expect(screen.getByRole("alert").textContent).toContain(
        "Unable to check existing variables",
      ),
    )
    expect(mocks.envSet).not.toHaveBeenCalled()
    expect(mocks.envAttach).not.toHaveBeenCalled()
    expect(
      screen.getByRole("button", { name: "Save" }).hasAttribute("disabled"),
    ).toBe(false)
  })
  it("allows the same name in another context", async () => {
    mocks.envList.mockResolvedValueOnce([
      { name: "NEW", value: "staging-only", context: "staging" },
    ])
    render(AddDialog, { open: true })
    await fireEvent.input(screen.getByLabelText("Name"), {
      target: { value: "NEW" },
    })
    await fireEvent.input(screen.getByLabelText("Value"), {
      target: { value: "default-value" },
    })
    await fireEvent.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() =>
      expect(mocks.envSet).toHaveBeenCalledWith(
        "NEW",
        "default-value",
        "default",
      ),
    )
  })
  it("creates an empty value after a successful fresh duplicate check", async () => {
    render(AddDialog, { open: true })
    await fireEvent.input(screen.getByLabelText("Name"), {
      target: { value: "EMPTY" },
    })
    await fireEvent.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() =>
      expect(mocks.envSet).toHaveBeenCalledWith("EMPTY", "", "default"),
    )
    expect(mocks.envList).toHaveBeenCalledOnce()
    expect(mocks.envList.mock.invocationCallOrder[0]).toBeLessThan(
      mocks.envSet.mock.invocationCallOrder[0],
    )
  })
  it("reports saved variable with failed attachment and prevents duplicate resubmit", async () => {
    mocks.envAttach.mockRejectedValueOnce(new Error("offline"))
    render(AddDialog, { open: true })
    await fireEvent.input(screen.getByLabelText("Name"), {
      target: { value: "NEW" },
    })
    await fireEvent.click(
      screen.getByRole("switch", { name: "Inject into workspaces" }),
    )
    await fireEvent.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() =>
      expect(screen.getByRole("alert").textContent).toContain("was saved"),
    )
    expect(mocks.envSet).toHaveBeenCalledWith("NEW", "", "default")
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull()
  })
  it("restores attachment state on failed mutation", async () => {
    mocks.envAttach.mockRejectedValueOnce(new Error("offline"))
    render(EnvPage)
    const toggle = screen.getAllByRole("switch", {
      name: "Inject SHARED into workspaces",
    })[1]
    await fireEvent.click(toggle)
    await waitFor(() =>
      expect(screen.getByRole("alert").textContent).toContain(
        "could not be updated",
      ),
    )
    expect(
      screen
        .getAllByRole("switch", { name: "Inject SHARED into workspaces" })[1]
        .getAttribute("aria-checked"),
    ).toBe("false")
  })
})

it("keeps create and attachment in the context captured when add opened", async () => {
  let complete!: () => void
  mocks.envSet.mockImplementationOnce(
    () =>
      new Promise<void>((resolve) => {
        complete = resolve
      }),
  )
  envVars.set([])
  activeContext.set("default")
  render(AddDialog, { open: true })
  activeContext.set("staging")
  await fireEvent.input(screen.getByLabelText("Name"), {
    target: { value: "CAPTURED" },
  })
  await fireEvent.click(
    screen.getByRole("switch", { name: "Inject into workspaces" }),
  )
  await fireEvent.click(screen.getByRole("button", { name: "Save" }))
  activeContext.set("production")
  await waitFor(() =>
    expect(mocks.envSet).toHaveBeenCalledWith("CAPTURED", "", "default"),
  )
  complete()
  await waitFor(() =>
    expect(mocks.envAttach).toHaveBeenCalledWith("CAPTURED", "default"),
  )
  expect(mocks.envSet).toHaveBeenCalledWith("CAPTURED", "", "default")
  cleanup()
})
