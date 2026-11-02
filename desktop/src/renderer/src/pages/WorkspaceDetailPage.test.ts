import { cleanup, fireEvent, render, screen } from "@testing-library/svelte"
import { tick } from "svelte"
import { get } from "svelte/store"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import type * as CommandsTypes from "$lib/ipc/commands.js"

type CommandsModule = typeof CommandsTypes

const { workspaceEdit, goto, copyText } = vi.hoisted(() => ({
  workspaceEdit: vi.fn(),
  goto: vi.fn(),
  copyText: vi.fn(async (_text: string) => {}),
}))

vi.mock("$lib/ipc/commands.js", async (importOriginal) => {
  const actual = await importOriginal<CommandsModule>()
  return {
    ...actual,
    workspaceEdit: (...args: unknown[]) => workspaceEdit(...args),
    workspaceUp: vi.fn(),
    workspaceStop: vi.fn(),
    workspaceRebuild: vi.fn(),
    workspaceReset: vi.fn(),
    workspaceDelete: vi.fn(),
    workspaceRename: vi.fn(),
    workspaceSetIde: vi.fn(),
    workspaceStatus: vi.fn(async () => "{}"),
    workspaceLogsList: vi.fn(async () => []),
    workspaceLogRead: vi.fn(async () => ""),
    workspaceLogDelete: vi.fn(),
  }
})
vi.mock("$lib/ipc/events.js", () => ({
  onCommandProgress: vi.fn(async () => () => {}),
  onWorkspacesChanged: vi.fn(async () => () => {}),
}))
vi.mock("$lib/ipc/terminal.js", () => ({
  terminalCreateSsh: vi.fn(),
  terminalClose: vi.fn(),
}))
vi.mock("$lib/router.js", async () => {
  const { writable } = await import("svelte/store")
  return { goto, querystring: writable("") }
})
vi.mock("$lib/components/terminal/Terminal.svelte", () => ({
  default: vi.fn(),
}))
vi.mock("$lib/components/log/LogTable.svelte", () => ({ default: vi.fn() }))

import { WorkspaceEditError } from "$lib/ipc/commands.js"
import { workspaces } from "$lib/stores/workspaces.js"
import WorkspaceDetailPage from "./WorkspaceDetailPageHarness.svelte"

const result = {
  id: "api",
  uid: "u1",
  context: "default",
  displayName: "Payments API",
  sshHostAlias: "pay",
  effectiveDisplayName: "Payments API",
  preferredSSHHost: "pay.devsy",
  canonicalSSHHost: "api.devsy",
  sshEntryUpdated: true,
}

function input(id: string): HTMLInputElement {
  return document.getElementById(id) as HTMLInputElement
}

async function openEditor() {
  await fireEvent.click(
    screen.getByRole("button", {
      name: "Edit display label and SSH alias",
    }),
  )
}

async function settle() {
  for (let i = 0; i < 5; i++) await tick()
}

describe("workspace detail presentation editing", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText: copyText },
      configurable: true,
    })
    workspaces.set([
      {
        id: "api",
        uid: "u1",
        context: "default",
        status: "Stopped",
        provider: { name: "docker" },
      },
    ])
  })
  afterEach(cleanup)

  it("shows the ID as the heading for old payloads and the canonical host for SSH", () => {
    render(WorkspaceDetailPage, { params: { id: "api" } })
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("api")
    expect(document.body.textContent).toContain("ssh api.devsy")
  })

  it("uses the label as heading, the preferred host for SSH, and keeps the ID visible", () => {
    workspaces.set([
      {
        id: "api",
        uid: "u1",
        context: "default",
        status: "Stopped",
        displayName: "Payments API",
        sshHostAlias: "pay",
      },
    ])
    render(WorkspaceDetailPage, { params: { id: "api" } })
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe(
      "Payments API",
    )
    expect(
      document.querySelector('[data-slot="workspace-id-caption"]')?.textContent,
    ).toBe("api")
    expect(document.body.textContent).toContain("ssh pay.devsy")
    expect(
      document.querySelector('[data-slot="workspace-canonical-host"]')
        ?.textContent,
    ).toBe("api.devsy")
  })

  it("does not persist anything while typing and cancels without saving", async () => {
    render(WorkspaceDetailPage, { params: { id: "api" } })
    await openEditor()
    await fireEvent.input(input("workspace-display-label"), {
      target: { value: "Draft" },
    })
    await fireEvent.input(input("workspace-ssh-alias"), {
      target: { value: "draft" },
    })
    expect(workspaceEdit).not.toHaveBeenCalled()
    expect(get(workspaces)[0].displayName).toBeUndefined()
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("api")
    expect(document.body.textContent).toContain(".devsy")
    await fireEvent.click(screen.getByRole("button", { name: "Cancel" }))
    expect(document.getElementById("workspace-display-label")).toBeNull()
    expect(workspaceEdit).not.toHaveBeenCalled()
  })

  it("sends only dirty fields with expected values and does not navigate", async () => {
    workspaceEdit.mockResolvedValue(result)
    render(WorkspaceDetailPage, { params: { id: "api" } })
    await openEditor()
    await fireEvent.input(input("workspace-display-label"), {
      target: { value: "Payments API" },
    })
    await fireEvent.click(screen.getByRole("button", { name: "Save" }))
    await settle()
    expect(workspaceEdit).toHaveBeenCalledExactlyOnceWith({
      workspaceId: "api",
      expectedUid: "u1",
      context: "default",
      displayName: "Payments API",
      expectedDisplayName: "",
    })
    expect(goto).not.toHaveBeenCalled()
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe(
      "Payments API",
    )
    expect(document.getElementById("workspace-display-label")).toBeNull()
  })

  it("keeps the draft and shows a field error with aria-describedby on failure", async () => {
    workspaceEdit.mockRejectedValue(
      new WorkspaceEditError({
        field: "sshHostAlias",
        code: "conflict",
        message: "SSH host pay.devsy is used by workspace other.",
        conflict: "other",
      }),
    )
    render(WorkspaceDetailPage, { params: { id: "api" } })
    await openEditor()
    await fireEvent.input(input("workspace-display-label"), {
      target: { value: "Keep me" },
    })
    await fireEvent.input(input("workspace-ssh-alias"), {
      target: { value: "pay" },
    })
    await fireEvent.click(screen.getByRole("button", { name: "Save" }))
    await settle()
    const alias = input("workspace-ssh-alias")
    expect(alias.value).toBe("pay")
    expect(input("workspace-display-label").value).toBe("Keep me")
    expect(alias.getAttribute("aria-invalid")).toBe("true")
    expect(alias.getAttribute("aria-describedby")).toBe(
      "workspace-ssh-alias-hint workspace-ssh-alias-error",
    )
    expect(
      document.getElementById("workspace-ssh-alias-error")?.textContent,
    ).toContain("used by workspace other")
    expect(
      input("workspace-display-label").getAttribute("aria-invalid"),
    ).toBeNull()
    expect(
      (screen.getByRole("button", { name: "Save" }) as HTMLButtonElement)
        .disabled,
    ).toBe(false)
  })

  it("validates locally before calling the backend", async () => {
    render(WorkspaceDetailPage, { params: { id: "api" } })
    await openEditor()
    await fireEvent.input(input("workspace-ssh-alias"), {
      target: { value: "Bad Alias" },
    })
    await fireEvent.click(screen.getByRole("button", { name: "Save" }))
    await settle()
    expect(workspaceEdit).not.toHaveBeenCalled()
    expect(
      document.getElementById("workspace-ssh-alias-error")?.textContent,
    ).toBeTruthy()
  })

  it("is busy only while the request is in flight", async () => {
    const pending = Promise.withResolvers<unknown>()
    workspaceEdit.mockReturnValue(pending.promise)
    render(WorkspaceDetailPage, { params: { id: "api" } })
    await openEditor()
    const save = screen.getByRole("button", {
      name: "Save",
    }) as HTMLButtonElement
    expect(save.disabled).toBe(false)
    await fireEvent.input(input("workspace-display-label"), {
      target: { value: "Payments API" },
    })
    expect(save.disabled).toBe(false)
    await fireEvent.click(save)
    await settle()
    expect(save.disabled).toBe(true)
    pending.resolve(result)
    await settle()
    await settle()
    expect(document.getElementById("workspace-display-label")).toBeNull()
  })

  it("keeps the permanent ID and the separate Change workspace ID action in details", () => {
    render(WorkspaceDetailPage, { params: { id: "api" } })
    expect(
      document.querySelector('[data-slot="workspace-id-value"]')?.textContent,
    ).toBe("api")
    expect(
      document.querySelector('[data-slot="workspace-rename-btn"]')?.textContent,
    ).toContain("Change workspace ID")
  })

  it("hides label editing for platform workspaces", () => {
    workspaces.set([
      {
        id: "api",
        uid: "u1",
        context: "default",
        status: "Stopped",
        pro: { displayName: "Team API" },
      },
    ])
    render(WorkspaceDetailPage, { params: { id: "api" } })
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe(
      "Team API",
    )
    expect(
      screen.queryByRole("button", {
        name: "Edit display label and SSH alias",
      }),
    ).toBeNull()
  })
})
