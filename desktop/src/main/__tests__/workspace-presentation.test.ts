// @vitest-environment node
import { join } from "node:path"
import { pathToFileURL } from "node:url"
import { beforeEach, describe, expect, it, vi } from "vitest"
import {
  buildWorkspaceEditArgs,
  buildWorkspacePresentationRequest,
  parseWorkspacePresentationResult,
  presentationFailureFromCliError,
  validateDisplayLabel,
  validateSshAlias,
  validateWorkspacePresentationRequest,
  type WorkspacePresentationBase,
} from "../../shared/workspace-presentation.js"
import { DaemonState } from "../state.js"

const handlers = new Map<string, (...args: unknown[]) => unknown>()

vi.mock("electron", () => ({
  app: {
    getPath: () => "/tmp",
    getAppPath: () => "/tmp",
    getVersion: () => "0.0.0",
  },
  dialog: {},
  ipcMain: {
    handle: (channel: string, fn: (...args: unknown[]) => unknown) => {
      handlers.set(channel, fn)
    },
    on: () => undefined,
  },
}))

vi.mock("../analytics.js", () => ({
  hashWorkspaceRef: (v: string) => v,
  trackEvent: () => undefined,
}))

const { registerIpcHandlers } = await import("../ipc.js")

const base: WorkspacePresentationBase = {
  id: "backend",
  uid: "uid-1",
  context: "work",
  displayName: "Old label",
  sshHostAlias: "old-alias",
}

const resultPayload = {
  id: "backend",
  uid: "uid-1",
  context: "work",
  displayName: "New label",
  sshHostAlias: "",
  effectiveDisplayName: "New label",
  preferredSSHHost: "backend.devsy",
  canonicalSSHHost: "backend.devsy",
  sshEntryUpdated: true,
}

describe("buildWorkspacePresentationRequest", () => {
  it("returns null when nothing changed", () => {
    expect(
      buildWorkspacePresentationRequest(base, {
        displayName: "  Old label ",
        sshHostAlias: "old-alias",
      }),
    ).toBeNull()
  })

  it("sends only the dirty display label with its expected value", () => {
    expect(
      buildWorkspacePresentationRequest(base, {
        displayName: "New label",
        sshHostAlias: "old-alias",
      }),
    ).toEqual({
      workspaceId: "backend",
      expectedUid: "uid-1",
      context: "work",
      displayName: "New label",
      expectedDisplayName: "Old label",
    })
  })

  it("sends only the dirty alias with its expected value", () => {
    const request = buildWorkspacePresentationRequest(base, {
      displayName: "Old label",
      sshHostAlias: "new-alias",
    })
    expect(request).toEqual({
      workspaceId: "backend",
      expectedUid: "uid-1",
      context: "work",
      sshHostAlias: "new-alias",
      expectedSshHostAlias: "old-alias",
    })
    expect(request).not.toHaveProperty("displayName")
  })

  it("sends an empty string to clear an override", () => {
    const request = buildWorkspacePresentationRequest(base, {
      displayName: "   ",
      sshHostAlias: "",
    })
    expect(request).toMatchObject({
      displayName: "",
      expectedDisplayName: "Old label",
      sshHostAlias: "",
      expectedSshHostAlias: "old-alias",
    })
  })

  it("does not treat an unset field as dirty when the draft is empty", () => {
    expect(
      buildWorkspacePresentationRequest(
        { ...base, displayName: "", sshHostAlias: "" },
        { displayName: "", sshHostAlias: "" },
      ),
    ).toBeNull()
  })
})

describe("buildWorkspaceEditArgs", () => {
  it("builds an argv array with the explicit context and only dirty flags", () => {
    expect(
      buildWorkspaceEditArgs(
        {
          workspaceId: "backend",
          expectedUid: "uid-1",
          displayName: "  New label ",
          expectedDisplayName: "Old label",
        },
        "work",
      ),
    ).toEqual([
      "--context",
      "work",
      "workspace",
      "edit",
      "backend",
      "--expected-uid",
      "uid-1",
      "--display-name",
      "New label",
      "--expected-display-name",
      "Old label",
      "--result-format",
      "json",
    ])
  })

  it("omits flags for untouched fields", () => {
    const args = buildWorkspaceEditArgs(
      {
        workspaceId: "backend",
        expectedUid: "uid-1",
        sshHostAlias: "dev",
        expectedSshHostAlias: "",
      },
      "work",
    )
    expect(args).toContain("--ssh-host-alias")
    expect(args).toContain("--expected-ssh-host-alias")
    expect(args).not.toContain("--display-name")
    expect(args).not.toContain("--expected-display-name")
  })

  it("passes an empty value as its own argv element to clear a field", () => {
    const args = buildWorkspaceEditArgs(
      {
        workspaceId: "backend",
        expectedUid: "uid-1",
        displayName: "",
        sshHostAlias: "",
      },
      "work",
    )
    expect(args[args.indexOf("--display-name") + 1]).toBe("")
    expect(args[args.indexOf("--ssh-host-alias") + 1]).toBe("")
    expect(args).not.toContain("--expected-display-name")
    expect(args).not.toContain("--expected-ssh-host-alias")
  })

  it("never splits values containing spaces or shell metacharacters", () => {
    const args = buildWorkspaceEditArgs(
      {
        workspaceId: "backend",
        expectedUid: "uid-1",
        displayName: "a b; rm -rf /",
      },
      "work",
    )
    expect(args[args.indexOf("--display-name") + 1]).toBe("a b; rm -rf /")
  })
})

describe("validation", () => {
  it.each([
    ["", null],
    ["My workspace", null],
    ["a".repeat(120), null],
    ["a".repeat(121), "at most 120"],
    ["bad\u0007label", "control characters"],
    ["tab\tinside", "control characters"],
  ])("display label %j", (value, expected) => {
    const message = validateDisplayLabel(value)
    if (expected === null) expect(message).toBeNull()
    else expect(message).toContain(expected)
  })

  it("counts code points rather than UTF-16 units", () => {
    expect(validateDisplayLabel("\u{1F600}".repeat(120))).toBeNull()
  })

  it.each([
    ["", null],
    ["dev", null],
    ["my-box-1", null],
    ["a".repeat(48), null],
    ["a".repeat(49), "at most 48"],
    ["My", "lowercase"],
    ["-dev", "lowercase"],
    ["dev-", "lowercase"],
    ["dev.devsy", "suffix"],
    ["a b", "spaces"],
    ["a.b", "dots"],
    ["a*", "wildcards"],
    ["a/b", "slashes"],
  ])("ssh alias %j", (value, expected) => {
    const message = validateSshAlias(value)
    if (expected === null) expect(message).toBeNull()
    else expect(message).toContain(expected)
  })

  it("rejects malformed requests", () => {
    expect(
      validateWorkspacePresentationRequest({
        workspaceId: "",
        expectedUid: "u",
        displayName: "x",
      }),
    ).toMatchObject({ field: "workspace", code: "invalid" })
    expect(
      validateWorkspacePresentationRequest({
        workspaceId: "a",
        expectedUid: "",
        displayName: "x",
      }),
    ).toMatchObject({ field: "workspace", code: "invalid" })
    expect(
      validateWorkspacePresentationRequest({
        workspaceId: "a",
        expectedUid: "u",
      }),
    ).toMatchObject({ field: "workspace", code: "invalid" })
    expect(
      validateWorkspacePresentationRequest({
        workspaceId: "a",
        expectedUid: "u",
        sshHostAlias: "x",
        expectedDisplayName: "stale",
      }),
    ).toMatchObject({ field: "workspace", code: "invalid" })
    expect(
      validateWorkspacePresentationRequest({
        workspaceId: "a",
        expectedUid: "u",
        displayName: 5 as unknown as string,
      }),
    ).toMatchObject({ field: "workspace", code: "invalid" })
  })

  it("reports field-specific validation failures", () => {
    expect(
      validateWorkspacePresentationRequest({
        workspaceId: "a",
        expectedUid: "u",
        sshHostAlias: "Bad Alias",
      }),
    ).toMatchObject({ field: "sshHostAlias", code: "invalid" })
    expect(
      validateWorkspacePresentationRequest({
        workspaceId: "a",
        expectedUid: "u",
        displayName: "a".repeat(200),
      }),
    ).toMatchObject({ field: "displayName", code: "invalid" })
  })

  it("accepts a clear-only request", () => {
    expect(
      validateWorkspacePresentationRequest({
        workspaceId: "a",
        expectedUid: "u",
        displayName: "",
      }),
    ).toBeNull()
  })
})

describe("result and failure parsing", () => {
  it("parses a complete result", () => {
    expect(parseWorkspacePresentationResult(resultPayload)).toEqual(
      resultPayload,
    )
  })

  it.each([null, [], "x", {}, { id: "a" }])("rejects %j", (value) => {
    expect(parseWorkspacePresentationResult(value)).toBeNull()
  })

  it("maps CLI errors to a field-specific failure", () => {
    expect(
      presentationFailureFromCliError(
        {
          code: "conflict",
          message: "taken",
          context: { field: "sshHostAlias", conflict: "other" },
        },
        "fallback",
      ),
    ).toEqual({
      field: "sshHostAlias",
      code: "conflict",
      message: "taken",
      conflict: "other",
    })
  })

  it("falls back to a workspace-level failure for unknown errors", () => {
    expect(presentationFailureFromCliError(undefined, "boom")).toEqual({
      field: "workspace",
      code: "failed",
      message: "boom",
    })
    expect(
      presentationFailureFromCliError(
        { code: "mystery", message: "", context: "flat" },
        "boom",
      ),
    ).toEqual({ field: "workspace", code: "failed", message: "boom" })
  })
})

function setup(runImpl: (args: string[]) => Promise<unknown>) {
  const state = new DaemonState()
  vi.spyOn(state, "workspaceContext").mockReturnValue("fallback-ctx")
  const cli = {
    setUnlockHandler: vi.fn(),
    run: vi.fn(runImpl),
    runRaw: vi.fn(async () => ""),
    cancelFor: vi.fn(async () => undefined),
  }
  const mainFrame = {
    url: pathToFileURL(join(__dirname, "../../renderer/index.html")).href,
    isDestroyed: () => false,
  }
  const webContents = { send: vi.fn(), mainFrame, isDestroyed: () => false }
  const refreshWorkspaces = vi.fn(async () => undefined)
  registerIpcHandlers({
    cli,
    state,
    logStore: {},
    pty: {},
    getMainWindow: () => ({ webContents, isDestroyed: () => false }),
    providerJobs: { onChange: vi.fn() },
    workspaceJobs: {},
    refreshWorkspaces,
    // biome-ignore lint/suspicious/noExplicitAny: partial test doubles
  } as any)
  return { cli, refreshWorkspaces }
}

function invokeEdit(request: unknown) {
  const handler = handlers.get("workspace_edit")
  if (!handler) throw new Error("workspace_edit not registered")
  return handler({}, request)
}

describe("workspace_edit IPC", () => {
  beforeEach(() => {
    handlers.clear()
    vi.clearAllMocks()
  })

  it("runs the CLI with an argv array and the captured context", async () => {
    const { cli, refreshWorkspaces } = setup(async () => resultPayload)
    const reply = await invokeEdit({
      workspaceId: "backend",
      expectedUid: "uid-1",
      context: "work",
      displayName: "New label",
      expectedDisplayName: "Old label",
    })
    expect(cli.run).toHaveBeenCalledExactlyOnceWith([
      "--context",
      "work",
      "workspace",
      "edit",
      "backend",
      "--expected-uid",
      "uid-1",
      "--display-name",
      "New label",
      "--expected-display-name",
      "Old label",
      "--result-format",
      "json",
    ])
    expect(reply).toEqual({ ok: true, result: resultPayload })
    expect(refreshWorkspaces).toHaveBeenCalledOnce()
  })

  it("falls back to the workspace context from state", async () => {
    const { cli } = setup(async () => ({
      ...resultPayload,
      context: "fallback-ctx",
    }))
    await invokeEdit({
      workspaceId: "backend",
      expectedUid: "uid-1",
      sshHostAlias: "dev",
    })
    expect(cli.run.mock.calls[0][0].slice(0, 2)).toEqual([
      "--context",
      "fallback-ctx",
    ])
  })

  it("rejects invalid requests without spawning the CLI", async () => {
    const { cli } = setup(async () => resultPayload)
    const reply = await invokeEdit({
      workspaceId: "backend",
      expectedUid: "uid-1",
      sshHostAlias: "Not Valid",
    })
    expect(reply).toMatchObject({
      ok: false,
      field: "sshHostAlias",
      code: "invalid",
    })
    expect(cli.run).not.toHaveBeenCalled()
  })

  it("does not apply a result that belongs to another workspace or context", async () => {
    const { refreshWorkspaces } = setup(async () => ({
      ...resultPayload,
      context: "other",
    }))
    const reply = await invokeEdit({
      workspaceId: "backend",
      expectedUid: "uid-1",
      context: "work",
      displayName: "x",
    })
    expect(reply).toMatchObject({ ok: false, code: "stale" })
    expect(refreshWorkspaces).not.toHaveBeenCalled()
  })

  it("surfaces structured CLI failures with their field", async () => {
    setup(async () => {
      throw Object.assign(new Error("taken"), {
        cliError: {
          code: "conflict",
          message: "Display label is used by other",
          context: { field: "displayName", conflict: "other" },
        },
      })
    })
    const reply = await invokeEdit({
      workspaceId: "backend",
      expectedUid: "uid-1",
      context: "work",
      displayName: "x",
    })
    expect(reply).toEqual({
      ok: false,
      field: "displayName",
      code: "conflict",
      message: "Display label is used by other",
      conflict: "other",
    })
  })
})
