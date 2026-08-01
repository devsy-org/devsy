import type {
  AuditEntry,
  Context,
  EnvVar,
  LoadCatalogResult,
  LogEntry,
  Machine,
  OptionValue,
  Provider,
  ProviderOption,
  ProviderVersion,
  ProviderVersionCheckResult,
  Secret,
  SshKeyInfo,
  Workspace,
} from "$lib/types/index.js"
import { invoke } from "./bridge.js"

type CommandEnvelope =
  | { ok: true }
  | { ok: false; message: string; cliError?: import("$shared/cli-error.js").CLIError }

/** Unwrap a structured command envelope, rethrowing failures as an Error with .cliError attached. */
function unwrapEnvelope(result: CommandEnvelope): void {
  if (result.ok) return
  const err = new Error(result.message) as Error & {
    cliError?: import("$shared/cli-error.js").CLIError
  }
  if (result.cliError) err.cliError = result.cliError
  throw err
}

// Workspace commands
export async function workspaceList(): Promise<Workspace[]> {
  return invoke<Workspace[]>("workspace_list")
}

export async function workspaceUp(params: {
  source: string
  workspaceId?: string
  provider?: string
  ide?: string
  ideLaunch?: "auto" | "headless" | "skip"
  debug?: boolean
  workspaceFolder?: string
  devcontainer?: string
  prebuildRepository?: string
  platform?: string
  recovery?: boolean
}): Promise<string> {
  return invoke<string>("workspace_up", params)
}

export async function workspaceStop(
  workspaceId: string,
  debug?: boolean,
): Promise<string> {
  return invoke<string>("workspace_stop", { workspaceId, debug })
}

export async function workspaceDelete(
  workspaceId: string,
  debug?: boolean,
): Promise<string> {
  return invoke<string>("workspace_delete", { workspaceId, debug })
}

export async function workspaceRebuild(
  workspaceId: string,
  debug?: boolean,
): Promise<string> {
  return invoke<string>("workspace_rebuild", { workspaceId, debug })
}

export async function workspaceReset(
  workspaceId: string,
  debug?: boolean,
): Promise<string> {
  return invoke<string>("workspace_reset", { workspaceId, debug })
}

export async function workspaceStatus(
  workspaceId: string,
  recovery = false,
): Promise<string> {
  return invoke<string>("workspace_status", { workspaceId, recovery })
}

export async function workspaceRename(
  workspaceId: string,
  newWorkspaceId: string,
): Promise<void> {
  return invoke("workspace_rename", { workspaceId, newWorkspaceId })
}

export async function workspaceSetIde(
  workspaceId: string,
  ide: string,
): Promise<void> {
  return invoke("workspace_set_ide", { workspaceId, ide })
}

export async function openDirectoryDialog(): Promise<string | null> {
  return invoke<string | null>("dialog_open_directory")
}

// Provider commands
export async function providerList(): Promise<Provider[]> {
  return invoke<Provider[]>("provider_list")
}

export async function providerAdd(
  name: string,
  source?: string,
  singleMachine?: boolean,
): Promise<void> {
  return invoke("provider_add", { name, source, singleMachine })
}

export async function providerSetSingleMachine(
  name: string,
  enabled: boolean,
): Promise<void> {
  return invoke("provider_set_single_machine", { name, enabled })
}

export async function providerDelete(name: string): Promise<void> {
  return invoke("provider_delete", { name })
}

export async function providerUse(name: string): Promise<void> {
  return invoke("provider_use", { name })
}

export async function providerInit(name: string): Promise<void> {
  unwrapEnvelope(await invoke<CommandEnvelope>("provider_init", { name }))
}

export async function providerInitStreaming(name: string): Promise<string> {
  return invoke<string>("provider_init_streaming", { name })
}

/**
 * Release a provider job the caller opened but will not finish, e.g. when the
 * user skips initialization or abandons the add wizard after the install.
 */
export async function providerReleaseJob(name: string): Promise<void> {
  return invoke<void>("provider_release_job", { name })
}

export async function providerUpdate(name: string): Promise<void> {
  return invoke("provider_update", { name })
}

export async function providerOptions(
  name: string,
): Promise<Record<string, ProviderOption>> {
  return invoke<Record<string, ProviderOption>>("provider_options", { name })
}

export async function providerSetOptions(
  name: string,
  options: Record<string, OptionValue>,
): Promise<void> {
  const optionArgs = Object.entries(options).map(
    ([key, val]) => `${key}=${val}`,
  )
  return invoke("provider_set_options", { name, options: optionArgs })
}

export async function providerRename(
  name: string,
  newName: string,
): Promise<void> {
  return invoke("provider_rename", { name, newName })
}

export async function providerListVersions(name: string, noCache?: boolean) {
  return invoke<{ versions: ProviderVersion[]; unsupported: boolean; error?: string }>(
    "provider_list_versions",
    { name, noCache },
  )
}

export async function providerSetVersion(name: string, tag: string): Promise<void> {
  return invoke("provider_set_version", { name, tag })
}

export async function providerCheckUpdates() {
  return invoke<Record<string, ProviderVersionCheckResult>>("provider_check_updates")
}

// Image catalog commands
export async function imageCatalogGet(): Promise<LoadCatalogResult> {
  return invoke<LoadCatalogResult>("image_catalog_get")
}

export async function imageCatalogRefresh(): Promise<LoadCatalogResult> {
  return invoke<LoadCatalogResult>("image_catalog_refresh")
}

/** Host container platform as `os/arch` (e.g. "linux/arm64"). */
export async function getHostPlatform(): Promise<string> {
  return invoke<string>("get_host_platform")
}

/** Supported `os/arch` platforms for an image ref, via the registry manifest. */
export async function getImagePlatforms(ref: string): Promise<string[]> {
  return invoke<string[]>("image_inspect_platforms", { ref })
}

// Machine commands
export async function machineList(): Promise<Machine[]> {
  return invoke<Machine[]>("machine_list")
}

export async function machineCreate(
  name: string,
  provider: string,
  options?: Record<string, OptionValue>,
): Promise<void> {
  return invoke("machine_create", { name, provider, options })
}

export async function machineDelete(
  id: string,
  force?: boolean,
): Promise<void> {
  return invoke("machine_delete", { id, force: force ?? false })
}

export async function machineStart(id: string): Promise<void> {
  return invoke("machine_start", { id })
}

export async function machineStop(id: string): Promise<void> {
  return invoke("machine_stop", { id })
}

export async function machineStatus(id: string): Promise<string> {
  const raw = await invoke<string>("machine_status", { id })
  try {
    const parsed = JSON.parse(raw) as { state?: string }
    return parsed.state ?? ""
  } catch {
    return raw.trim()
  }
}

// Context commands
export async function contextList(): Promise<{
  contexts: Context[]
  activeContext: string
}> {
  return invoke("context_list")
}

export async function contextUse(name: string): Promise<void> {
  return invoke("context_use", { name })
}

export async function contextOptions(
  context?: string,
): Promise<Record<string, { value?: string }>> {
  return invoke("context_options", { context })
}

export async function contextSetOptions(
  options: string[],
  context?: string,
): Promise<void> {
  return invoke("context_set_options", { options, context })
}

export async function contextCreate(name: string): Promise<void> {
  return invoke("context_create", { name })
}

export async function contextDelete(name: string): Promise<void> {
  return invoke("context_delete", { name })
}

export async function secretList(): Promise<Secret[]> {
  return invoke<Secret[]>("secret_list")
}

export async function secretSet(name: string, value: string): Promise<void> {
  unwrapEnvelope(await invoke<CommandEnvelope>("secret_set", { name, value }))
}

export async function secretDelete(name: string): Promise<void> {
  unwrapEnvelope(await invoke<CommandEnvelope>("secret_delete", { name }))
}

export async function envList(): Promise<EnvVar[]> {
  return invoke<EnvVar[]>("env_list")
}

export async function envSet(name: string, value: string): Promise<void> {
  unwrapEnvelope(await invoke<CommandEnvelope>("env_set", { name, value }))
}

export async function envDelete(name: string): Promise<void> {
  unwrapEnvelope(await invoke<CommandEnvelope>("env_delete", { name }))
}

// Audit commands
export async function auditRecent(limit?: number): Promise<AuditEntry[]> {
  return invoke<AuditEntry[]>("audit_recent", { limit })
}

export async function auditByResource(
  resourceType: string,
  resourceId: string,
  limit?: number,
): Promise<AuditEntry[]> {
  return invoke<AuditEntry[]>("audit_by_resource", {
    resourceType,
    resourceId,
    limit,
  })
}

// App lifecycle
export async function appReady(): Promise<void> {
  return invoke<void>("app_ready")
}

// System commands
export async function devsyVersion(): Promise<string> {
  return invoke<string>("devsy_version")
}

export async function devsyUpgrade(version: string): Promise<string> {
  return invoke<string>("devsy_upgrade", { version })
}

export async function devsyUpgradeDryRun(version: string): Promise<string> {
  return invoke<string>("devsy_upgrade_dry_run", { version })
}

// Log commands
export async function workspaceLogsList(
  workspaceId: string,
): Promise<LogEntry[]> {
  return invoke<LogEntry[]>("workspace_logs_list", { workspaceId })
}

export async function workspaceLogRead(
  workspaceId: string,
  filename: string,
): Promise<string> {
  return invoke<string>("workspace_log_read", { workspaceId, filename })
}

export async function workspaceLogDelete(
  workspaceId: string,
  filename: string,
): Promise<void> {
  return invoke<void>("workspace_log_delete", { workspaceId, filename })
}

// SSH key commands
export async function sshKeyList(): Promise<SshKeyInfo[]> {
  return invoke<SshKeyInfo[]>("ssh_key_list")
}

export async function sshKeyGenerate(params: {
  name: string
  keyType?: string
  comment?: string
}): Promise<SshKeyInfo> {
  return invoke<SshKeyInfo>("ssh_key_generate", params)
}

// Release channel
export type ReleaseChannel = "stable" | "beta"

export async function getReleaseChannel(): Promise<ReleaseChannel> {
  return invoke<ReleaseChannel>("get_release_channel")
}

export async function setReleaseChannel(channel: ReleaseChannel): Promise<void> {
  return invoke("set_release_channel", { channel })
}

export async function checkForUpdates(): Promise<void> {
  return invoke("check_for_updates")
}

export async function installUpdate(): Promise<void> {
  return invoke("install_update")
}

export async function downloadUpdate(): Promise<void> {
  return invoke("download_update")
}

export async function getAppVersion(): Promise<string> {
  return invoke<string>("get_app_version")
}

export async function getAutoDownload(): Promise<boolean> {
  return invoke<boolean>("get_auto_download")
}

export async function setAutoDownload(enabled: boolean): Promise<void> {
  return invoke("set_auto_download", { enabled })
}

// Analytics
export function analyticsTrack(
  name: string,
  properties?: Record<string, unknown>,
): void {
  invoke("analytics_track", { name, properties }).catch(() => {})
}
