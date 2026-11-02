/**
 * Mock IPC layer for browser-only development.
 * When Electron is not available (no window.electronAPI), this module
 * provides fake invoke() and listen() that return realistic sample data.
 *
 * Usage: `npm run dev` opens http://localhost:1420 in any browser.
 */

import type {
  Context,
  LogEntry,
  Machine,
  Provider,
  ProviderOption,
  SshKeyInfo,
  Workspace,
} from "$lib/types/index.js"
import type { LinuxDesktopIntegrationStatus } from "$shared/linux-desktop-integration.js"
import { workspaceLabel } from "$shared/workspace-label.js"
import type {
  LogIndexResponse,
  LogReadResponse,
  LogRecord,
  LogRunSummary,
} from "$shared/workspace-logs.js"
import {
  normalizeDisplayLabel,
  SSH_HOST_SUFFIX,
  validateWorkspacePresentationRequest,
  type WorkspacePresentationFailure,
  type WorkspacePresentationReply,
  type WorkspacePresentationRequest,
} from "$shared/workspace-presentation.js"

// ── Sample data ──────────────────────────────────────────────────────

const now = new Date().toISOString()
const hourAgo = new Date(Date.now() - 3600_000).toISOString()
const dayAgo = new Date(Date.now() - 86400_000).toISOString()

// ── Retained build and lifecycle history ─────────────────────────────

export const MOCK_RUN_FAILED = "11111111-1111-4111-8111-111111111111"
export const MOCK_RUN_OK = "22222222-2222-4222-8222-222222222222"
export const MOCK_RUN_PARTIAL = "33333333-3333-4333-8333-333333333333"
export const MOCK_RUN_REMOVED = "44444444-4444-4444-8444-444444444444"
export const MOCK_INV_POST_CREATE_FAILED =
  "a1111111-0000-4000-8000-000000000001"
export const MOCK_INV_POST_START_SKIPPED =
  "a1111111-0000-4000-8000-000000000002"
export const MOCK_INV_SILENT_OK = "a2222222-0000-4000-8000-000000000001"
export const MOCK_INV_POST_CREATE_OK = "a2222222-0000-4000-8000-000000000002"
export const MOCK_INV_DEFERRED = "a2222222-0000-4000-8000-000000000003"
export const MOCK_INV_UNCAPTURED = "a3333333-0000-4000-8000-000000000001"
export const MOCK_INV_REMOVED = "a4444444-0000-4000-8000-000000000001"

const MOCK_PRODUCER = "b0000000-0000-4000-8000-000000000001"
const MOCK_PRODUCER_LATE = "b0000000-0000-4000-8000-000000000002"

function runSummary(
  runId: string,
  patch: Partial<LogRunSummary>,
  minutesAgo: number,
): LogRunSummary {
  return {
    runId,
    context: "default",
    workspaceUid: "ws-001",
    workspaceIdAtStart: "my-project",
    command: "up",
    createdAt: new Date(Date.now() - minutesAgo * 60_000).toISOString(),
    commandState: "succeeded",
    captureState: "complete",
    ...patch,
  }
}

const MOCK_RUNS: LogRunSummary[] = [
  runSummary(MOCK_RUN_FAILED, { commandState: "failed" }, 5),
  runSummary(MOCK_RUN_OK, {}, 90),
  runSummary(
    MOCK_RUN_PARTIAL,
    { captureState: "partial", conditions: ["source_unavailable"] },
    600,
  ),
  runSummary(
    MOCK_RUN_REMOVED,
    {
      workspaceUid: "ws-gone",
      workspaceIdAtStart: "deleted-project",
      commandState: "failed",
    },
    2000,
  ),
]

type RecordPatch = Partial<LogRecord> & Pick<LogRecord, "kind">

function builder(runId: string, producerId = MOCK_PRODUCER) {
  let sequence = 0
  return (patch: RecordPatch): LogRecord => ({
    schemaVersion: 1,
    runId,
    producerId,
    sequence: ++sequence,
    time: new Date().toISOString(),
    ...patch,
  })
}

function failedRunRecords(): LogRecord[] {
  const r = builder(MOCK_RUN_FAILED)
  const inv = MOCK_INV_POST_CREATE_FAILED
  const base = {
    invocationId: inv,
    hook: "postCreateCommand",
    location: "container" as const,
  }
  return [
    r({ kind: "invocation", ...base, phase: "lifecycle", state: "running" }),
    r({
      kind: "output",
      invocationId: inv,
      stream: "stdout",
      text: "npm install\n",
      bytes: 12,
    }),
    r({
      kind: "output",
      invocationId: inv,
      stream: "stderr",
      text: '{"level":"error","msg":"not a log line"}\n<b>literal</b>\n',
      bytes: 52,
    }),
    r({
      kind: "invocation",
      ...base,
      phase: "lifecycle",
      state: "failed",
      exitCode: 1,
      durationMillis: 2300,
    }),
    r({
      kind: "invocation",
      invocationId: MOCK_INV_POST_START_SKIPPED,
      hook: "postStartCommand",
      phase: "lifecycle",
      state: "skipped",
      reason: "already_attempted",
    }),
  ]
}

function okRunRecords(): LogRecord[] {
  const r = builder(MOCK_RUN_OK)
  const late = builder(MOCK_RUN_OK, MOCK_PRODUCER_LATE)
  return [
    r({
      kind: "invocation",
      invocationId: MOCK_INV_SILENT_OK,
      hook: "onCreateCommand",
      phase: "lifecycle",
      state: "succeeded",
      exitCode: 0,
      durationMillis: 40,
    }),
    r({
      kind: "invocation",
      invocationId: MOCK_INV_POST_CREATE_OK,
      hook: "postCreateCommand",
      phase: "lifecycle",
      state: "running",
    }),
    r({
      kind: "output",
      invocationId: MOCK_INV_POST_CREATE_OK,
      stream: "stdout",
      text: "installed 12 packages\n\n  indented line\nno trailing newline",
      bytes: 54,
    }),
    r({
      kind: "invocation",
      invocationId: MOCK_INV_POST_CREATE_OK,
      hook: "postCreateCommand",
      phase: "lifecycle",
      state: "succeeded",
      exitCode: 0,
      durationMillis: 1800,
    }),
    late({
      kind: "invocation",
      invocationId: MOCK_INV_DEFERRED,
      hook: "postAttachCommand",
      phase: "lifecycle",
      state: "deferred",
    }),
  ]
}

function partialRunRecords(): LogRecord[] {
  const r = builder(MOCK_RUN_PARTIAL)
  return [
    r({
      kind: "invocation",
      invocationId: MOCK_INV_UNCAPTURED,
      hook: "postCreateCommand",
      phase: "lifecycle",
      state: "succeeded",
      exitCode: 0,
    }),
    r({
      kind: "capture",
      condition: "source_unavailable",
      detail: "container removed",
    }),
  ]
}

function removedRunRecords(): LogRecord[] {
  const r = builder(MOCK_RUN_REMOVED)
  return [
    r({
      kind: "invocation",
      invocationId: MOCK_INV_REMOVED,
      hook: "postCreateCommand",
      phase: "lifecycle",
      state: "failed",
      exitCode: 2,
    }),
    r({
      kind: "output",
      invocationId: MOCK_INV_REMOVED,
      stream: "stderr",
      text: "build failed\n",
      bytes: 13,
    }),
  ]
}

const MOCK_RUN_RECORDS: Record<string, () => LogRecord[]> = {
  [MOCK_RUN_FAILED]: failedRunRecords,
  [MOCK_RUN_OK]: okRunRecords,
  [MOCK_RUN_PARTIAL]: partialRunRecords,
  [MOCK_RUN_REMOVED]: removedRunRecords,
}

export function mockLogRuns(args: Record<string, unknown>): LogIndexResponse {
  const uid = args.workspaceUid as string | undefined
  const runs = MOCK_RUNS.filter(
    (run) =>
      run.context === args.context &&
      (!uid || run.workspaceUid === uid) &&
      (args.workspaceId ? run.workspaceUid !== "ws-gone" : true),
  )
  return { schemaVersion: 1, runs, hasMore: false }
}

export function mockLogRunRead(args: Record<string, unknown>): LogReadResponse {
  const run = MOCK_RUNS.find((candidate) => candidate.runId === args.runId)
  const records = MOCK_RUN_RECORDS[args.runId as string]
  if (!run || !records) throw new Error("run not found")
  const hook = args.hook as string | undefined
  const invocationId = args.invocationId as string | undefined
  const all = records()
  const wanted = new Set(
    all
      .filter(
        (r) =>
          r.kind === "invocation" &&
          (!hook || r.hook === hook) &&
          (!invocationId || r.invocationId === invocationId),
      )
      .map((r) => r.invocationId),
  )
  if (invocationId && wanted.size === 0) throw new Error("invocation not found")
  const filtered =
    hook || invocationId
      ? all.filter((r) => r.kind === "capture" || wanted.has(r.invocationId))
      : all
  const complete = run.captureState === "complete" && run.runId !== MOCK_RUN_OK
  return {
    schemaVersion: 1,
    run,
    records: filtered,
    hasMore: false,
    complete,
    availability:
      run.captureState === "partial"
        ? [
            {
              producerId: MOCK_PRODUCER,
              available: false,
              reason: "source_unavailable",
            },
          ]
        : undefined,
  }
}

const WORKSPACES: Workspace[] = [
  {
    id: "my-project",
    uid: "ws-001",
    provider: { name: "docker" },
    ide: { name: "vscode" },
    source: { gitRepository: "github.com/acme/my-project", gitBranch: "main" },
    status: "Running",
    creationTimestamp: dayAgo,
    lastUsed: hourAgo,
    context: "default",
  },
  {
    id: "backend-api",
    uid: "ws-002",
    provider: { name: "kubernetes" },
    ide: { name: "goland" },
    source: { gitRepository: "github.com/acme/backend-api" },
    status: "Stopped",
    creationTimestamp: dayAgo,
    lastUsed: dayAgo,
    context: "default",
  },
  {
    id: "ml-notebook",
    uid: "ws-003",
    provider: { name: "docker" },
    ide: { name: "jupyternotebook" },
    source: { image: "jupyter/scipy-notebook:latest" },
    status: "Running",
    creationTimestamp: hourAgo,
    lastUsed: now,
    context: "default",
  },
]

const PROVIDERS: Provider[] = [
  {
    name: "docker",
    version: "v0.5.0",
    source: { github: "devsy-org/devsy-provider-docker" },
    description: "Devsy on Docker",
    isDefault: true,
    state: { initialized: true, singleMachine: true },
  },
  {
    name: "podman",
    version: "v0.0.1",
    source: { github: "devsy-org/devsy-provider-podman" },
    description: "Devsy on Podman",
    state: { initialized: false, singleMachine: true },
  },
  {
    name: "kubernetes",
    version: "v0.3.2",
    source: { github: "devsy-org/devsy-provider-kubernetes" },
    description: "Devsy on Kubernetes",
    state: { initialized: true, singleMachine: false },
  },
  {
    name: "aws",
    version: "v0.4.1",
    source: { github: "devsy-org/devsy-provider-aws" },
    description: "Devsy on AWS",
    state: { initialized: false, singleMachine: false },
  },
]

const AVAILABLE_PROVIDERS = ["docker", "terraform"]

const MACHINES: Machine[] = [
  {
    id: "docker-desktop",
    provider: { name: "docker" },
    status: "Running",
    creationTimestamp: dayAgo,
    lastUsed: hourAgo,
  },
  {
    id: "k8s-cluster-1",
    provider: { name: "kubernetes" },
    status: "Running",
    creationTimestamp: dayAgo,
    lastUsed: now,
  },
]

let MOCK_APP_SETTINGS = {
  settings: {
    runAtStartup: false,
    openToTrayOnStartup: false,
    trayNotifications: "failures",
    desktopLogLevel: "info",
    cliCaptureLogLevel: "info",
    appImageIntegrationPromptDismissed: false,
  },
  startup: { applied: true, enabled: false, status: "disabled" },
} as {
  settings: {
    runAtStartup: boolean
    openToTrayOnStartup: boolean
    trayNotifications: "off" | "failures" | "all"
    desktopLogLevel: "error" | "warn" | "info" | "debug" | "trace"
    cliCaptureLogLevel: "error" | "warn" | "info" | "debug" | "trace"
    appImageIntegrationPromptDismissed: boolean
  }
  startup: {
    applied: boolean
    enabled: boolean
    status: "enabled" | "disabled" | "denied" | "unavailable" | "error"
    detail?: string
  }
}

// The browser mock behaves like a Devsy AppImage that was started directly.
let MOCK_LINUX_INTEGRATION: LinuxDesktopIntegrationStatus = {
  supported: true,
  appImagePath: "/home/user/Applications/Devsy.AppImage",
  installed: false,
  needsUpdate: false,
}

const CONTEXTS: Context[] = [{ name: "default" }, { name: "staging" }]

const SSH_KEYS: SshKeyInfo[] = [
  {
    name: "id_ed25519",
    keyType: "ed25519",
    fingerprint: "SHA256:abc123def456ghi789jkl012mno345pqr678stu901vwx",
    comment: "user@example.com",
    publicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI... user@example.com",
    path: "/home/user/.ssh/id_ed25519",
    hasPassphrase: false,
  },
  {
    name: "id_rsa",
    keyType: "rsa",
    fingerprint: "SHA256:xyz789abc012def345ghi678jkl901mno234pqr567stu",
    comment: "deploy@company.com",
    publicKey: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAB... deploy@company.com",
    path: "/home/user/.ssh/id_rsa",
    hasPassphrase: true,
  },
]

const PROVIDER_OPTIONS: Record<string, ProviderOption> = {
  DOCKER_HOST: {
    name: "DOCKER_HOST",
    displayName: "Docker Host",
    description: "Docker daemon socket to connect to",
    default: "unix:///var/run/docker.sock",
    value: "unix:///var/run/docker.sock",
    type: "string",
  },
  DISK_SIZE: {
    name: "DISK_SIZE",
    displayName: "Disk Size",
    description: "Size of the disk in GB",
    default: 30,
    value: 30,
    type: "number",
  },
}

const LOG_ENTRIES: LogEntry[] = [
  {
    workspaceId: "my-project",
    filename: "2026-04-16T10-00-00.log",
    createdAt: hourAgo,
    sizeBytes: 4096,
  },
  {
    workspaceId: "my-project",
    filename: "2026-04-15T08-30-00.log",
    createdAt: dayAgo,
    sizeBytes: 2048,
  },
]

// ── Counter for fake IDs ─────────────────────────────────────────────

let counter = 0
function nextId(): string {
  return `mock-${++counter}-${Date.now().toString(36)}`
}

function mockFailure(
  failure: WorkspacePresentationFailure,
): WorkspacePresentationReply {
  return { ok: false, ...failure }
}

type MockWorkspace = (typeof WORKSPACES)[number]

function staleFailure(
  request: WorkspacePresentationRequest,
  target: MockWorkspace,
): WorkspacePresentationFailure | null {
  if (
    request.expectedDisplayName !== undefined &&
    request.expectedDisplayName !== (target.displayName ?? "")
  ) {
    return {
      field: "displayName",
      code: "stale",
      message: "The display label changed since you opened this page.",
    }
  }
  if (
    request.expectedSshHostAlias !== undefined &&
    request.expectedSshHostAlias !== (target.sshHostAlias ?? "")
  ) {
    return {
      field: "sshHostAlias",
      code: "stale",
      message: "The SSH alias changed since you opened this page.",
    }
  }
  return null
}

function labelConflictFailure(
  label: string,
  others: MockWorkspace[],
): WorkspacePresentationFailure | null {
  if (label === "") return null
  const clash = others.find(
    (w) => workspaceLabel(w).toLowerCase() === label.toLowerCase(),
  )
  if (!clash) return null
  return {
    field: "displayName",
    code: "conflict",
    message: `Display label is already used by workspace ${clash.id}.`,
    conflict: clash.id,
  }
}

function aliasConflictFailure(
  alias: string,
  others: MockWorkspace[],
): WorkspacePresentationFailure | null {
  if (alias === "") return null
  const host = `${alias}${SSH_HOST_SUFFIX}`.toLowerCase()
  const clash = others.find(
    (w) =>
      `${w.id}${SSH_HOST_SUFFIX}`.toLowerCase() === host ||
      (w.sshHostAlias &&
        `${w.sshHostAlias}${SSH_HOST_SUFFIX}`.toLowerCase() === host),
  )
  if (!clash) return null
  return {
    field: "sshHostAlias",
    code: "conflict",
    message: `SSH host ${host} is already used by workspace ${clash.id}.`,
    conflict: clash.id,
  }
}

function nextPresentation(
  request: WorkspacePresentationRequest,
  target: MockWorkspace,
): { label: string; alias: string } {
  const label =
    request.displayName === undefined
      ? (target.displayName ?? "")
      : normalizeDisplayLabel(request.displayName)
  const requestedAlias =
    request.sshHostAlias === undefined
      ? (target.sshHostAlias ?? "")
      : request.sshHostAlias.trim()
  return { label, alias: requestedAlias === target.id ? "" : requestedAlias }
}

function applyPresentation(
  target: MockWorkspace,
  label: string,
  alias: string,
): WorkspacePresentationReply {
  target.displayName = label || undefined
  target.sshHostAlias = alias || undefined
  target.canonicalSSHHost = `${target.id}${SSH_HOST_SUFFIX}`
  target.preferredSSHHost = `${alias || target.id}${SSH_HOST_SUFFIX}`
  return {
    ok: true,
    result: {
      id: target.id,
      uid: target.uid ?? "",
      context: target.context ?? "default",
      displayName: label,
      sshHostAlias: alias,
      effectiveDisplayName: label || target.id,
      preferredSSHHost: target.preferredSSHHost,
      canonicalSSHHost: target.canonicalSSHHost,
      sshEntryUpdated: alias !== "",
    },
  }
}

function mockWorkspaceEdit(
  request: WorkspacePresentationRequest,
): WorkspacePresentationReply {
  const invalid = validateWorkspacePresentationRequest(request)
  if (invalid) return mockFailure(invalid)
  const target = WORKSPACES.find((w) => w.id === request.workspaceId)
  if (!target || target.uid !== request.expectedUid) {
    return mockFailure({
      field: "workspace",
      code: "not-found",
      message: `Workspace ${request.workspaceId} was not found.`,
    })
  }
  const others = WORKSPACES.filter((w) => w.id !== target.id)
  const { label, alias } = nextPresentation(request, target)
  const failure =
    staleFailure(request, target) ??
    labelConflictFailure(label, others) ??
    aliasConflictFailure(alias, others)
  if (failure) return mockFailure(failure)
  return applyPresentation(target, label, alias)
}

// ── Mock command handlers ────────────────────────────────────────────

type Handler = (args: Record<string, unknown>) => unknown

const COMMANDS: Record<string, Handler> = {
  // Workspaces
  workspace_list: () => WORKSPACES,
  workspace_snapshot: () => ({ revision: 0, workspaces: WORKSPACES, jobs: {} }),
  workspace_refresh: () => undefined,
  workspace_up: () => nextId(),
  workspace_stop: () => nextId(),
  workspace_delete: () => nextId(),
  workspace_rebuild: () => nextId(),
  workspace_reset: () => nextId(),
  workspace_status: (args) => {
    const ws = WORKSPACES.find((w) => w.id === args.workspaceId)
    return ws?.status ?? "NotFound"
  },
  workspace_edit: (args) =>
    mockWorkspaceEdit(args as unknown as WorkspacePresentationRequest),

  // Providers
  provider_list: () => PROVIDERS,
  provider_available: () => AVAILABLE_PROVIDERS,
  provider_add: () => undefined,
  provider_delete: () => undefined,
  provider_use: () => undefined,
  provider_update: () => undefined,
  provider_update_streaming: () => `mock-provider-update-${Date.now()}`,
  provider_refresh_state: () => ({ ok: true }),
  provider_get_update_cache: () => ({ updates: {}, lastCheckedAt: hourAgo }),
  provider_options: () => PROVIDER_OPTIONS,
  provider_set_options: () => undefined,
  provider_release_job: () => undefined,

  // Machines
  machine_list: () => MACHINES,
  machine_create: () => undefined,
  machine_delete: () => undefined,
  machine_start: () => undefined,
  machine_stop: () => undefined,
  machine_status: (args) => {
    const m = MACHINES.find((machine) => machine.id === args.id)
    return m?.status ?? "Unknown"
  },
  machine_diagnostics_get: () => null,
  machine_diagnostics_refresh: () => ({
    response: {
      schemaVersion: 1,
      machine: { id: "", context: "default", provider: "", state: "Running" },
      source: { availability: "not_initialized", freshness: "unknown" },
      cursor: { state: "none" },
    },
    events: [],
    lastAttemptAt: new Date().toISOString(),
    historyGap: false,
  }),

  // Contexts
  context_list: () => ({ contexts: CONTEXTS, activeContext: "default" }),
  context_use: () => undefined,
  context_options: () => ({
    TELEMETRY: { value: "true" },
    AGENT_URL: { value: "" },
    DOTFILES_URL: { value: "" },
    DOTFILES_SCRIPT: { value: "" },
    SSH_INJECT_DOCKER_CREDENTIALS: { value: "true" },
    SSH_INJECT_GIT_CREDENTIALS: { value: "true" },
    GIT_SSH_SIGNATURE_FORWARDING: { value: "true" },
    SSH_AGENT_FORWARDING: { value: "true" },
    SSH_ADD_PRIVATE_KEYS: { value: "true" },
    SSH_STRICT_HOST_KEY_CHECKING: { value: "false" },
    GPG_AGENT_FORWARDING: { value: "false" },
    AGENT_INJECT_TIMEOUT: { value: "20" },
    REGISTRY_CACHE: { value: "" },
    EXIT_AFTER_TIMEOUT: { value: "true" },
    SSH_CONFIG_PATH: { value: "" },
    SSH_CONFIG_INCLUDE_PATH: { value: "" },
  }),
  context_set_options: () => undefined,
  context_create: (args) => {
    CONTEXTS.push({ name: args.name as string })
  },
  context_delete: (args) => {
    const idx = CONTEXTS.findIndex((c) => c.name === args.name)
    if (idx !== -1) CONTEXTS.splice(idx, 1)
  },

  // System
  devsy_version: () => "v0.6.0-mock",
  devsy_upgrade: () => "Successfully upgraded",
  devsy_upgrade_dry_run: (args) =>
    `asset_name=devsy-linux-arm64\nversion=${(args.version as string).replace(/^v/, "")}\nos=linux\narch=arm64\nurl=https://github.com/example/devsy/releases/download/${args.version}/devsy-linux-arm64\nsize=110624952`,

  // Logs
  workspace_logs_list: () => LOG_ENTRIES,
  workspace_log_read: () =>
    "2026-04-16 10:00:01 Starting workspace...\n2026-04-16 10:00:03 Pulling image...\n2026-04-16 10:00:15 Container created\n2026-04-16 10:00:16 Workspace ready\n",

  workspace_log_runs: (args) => mockLogRuns(args),
  workspace_log_run_read: (args) => mockLogRunRead(args),
  workspace_log_run_cancel: () => undefined,
  workspace_log_run_export: () => ({ status: "canceled" }),

  // SSH Keys
  ssh_key_list: () => SSH_KEYS,
  ssh_key_generate: (args) => ({
    name: args.name as string,
    keyType: (args.keyType as string) ?? "ed25519",
    fingerprint: `SHA256:mock-${nextId()}`,
    comment: (args.comment as string) ?? "",
    publicKey: `ssh-ed25519 MOCK... ${args.comment ?? ""}`,
    path: `/home/user/.ssh/${args.name}`,
    hasPassphrase: false,
  }),

  // Terminals
  terminal_create: () => nextId(),
  terminal_create_ssh: () => nextId(),
  terminal_write: () => undefined,
  terminal_resize: () => undefined,
  terminal_close: () => undefined,
  terminal_list: () => [],

  get_host_platform: () => "linux/amd64",

  image_inspect_platforms: () => ["linux/amd64", "linux/arm64"],

  get_app_settings: () => MOCK_APP_SETTINGS,
  set_app_settings: (args) => {
    const patch = (args?.patch ?? {}) as Partial<
      typeof MOCK_APP_SETTINGS.settings
    >
    MOCK_APP_SETTINGS = {
      settings: {
        ...MOCK_APP_SETTINGS.settings,
        ...patch,
        openToTrayOnStartup:
          (patch.runAtStartup ?? MOCK_APP_SETTINGS.settings.runAtStartup) ===
          false
            ? false
            : (patch.openToTrayOnStartup ??
              MOCK_APP_SETTINGS.settings.openToTrayOnStartup),
      },
      startup: {
        applied: true,
        enabled: patch.runAtStartup ?? MOCK_APP_SETTINGS.settings.runAtStartup,
        status:
          (patch.runAtStartup ?? MOCK_APP_SETTINGS.settings.runAtStartup)
            ? ("enabled" as const)
            : ("disabled" as const),
      },
    }
    return MOCK_APP_SETTINGS
  },

  get_linux_desktop_integration: () => MOCK_LINUX_INTEGRATION,
  install_linux_desktop_integration: () => {
    MOCK_LINUX_INTEGRATION = {
      ...MOCK_LINUX_INTEGRATION,
      installed: true,
      needsUpdate: false,
    }
    return { status: MOCK_LINUX_INTEGRATION }
  },
  remove_linux_desktop_integration: () => {
    MOCK_LINUX_INTEGRATION = { ...MOCK_LINUX_INTEGRATION, installed: false }
    return { status: MOCK_LINUX_INTEGRATION }
  },

  // Release channel
  get_release_channel: () => "stable",
  set_release_channel: () => undefined,
  check_for_updates: () => undefined,
  install_update: () => undefined,

  // Analytics
  analytics_track: () => undefined,
}

// ── Mock invoke ──────────────────────────────────────────────────────

export async function invoke<T>(
  cmd: string,
  args?: Record<string, unknown>,
): Promise<T> {
  const handler = COMMANDS[cmd]
  if (!handler) {
    console.warn(`[mock] Unknown command: ${cmd}`)
    throw new Error(`Mock: unknown command "${cmd}"`)
  }

  // Simulate async delay
  await new Promise((r) => setTimeout(r, 50 + Math.random() * 100))

  const result = handler(args ?? {})
  console.debug(`[mock] ${cmd}`, args ?? {}, "->", result)
  return result as T
}

// ── Mock listen ──────────────────────────────────────────────────────

type UnlistenFn = () => void

export async function listen<T>(
  _event: string,
  _callback: (event: { payload: T }) => void,
): Promise<UnlistenFn> {
  // No-op in mock mode - events would come from the Rust backend
  return () => {}
}
