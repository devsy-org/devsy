import type {
  AuditEntry,
  Context,
  LogEntry,
  Machine,
  OptionValue,
  Provider,
  ProviderOption,
  SshKeyInfo,
  Workspace,
} from "$lib/types/index.js"
import { invoke } from "./bridge.js"

// Workspace commands
export async function workspaceList(): Promise<Workspace[]> {
  return invoke<Workspace[]>("workspace_list")
}

export async function workspaceUp(params: {
  source: string
  workspaceId?: string
  provider?: string
  ide?: string
  debug?: boolean
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

export async function workspaceStatus(workspaceId: string): Promise<string> {
  return invoke<string>("workspace_status", { workspaceId })
}

export async function workspaceRename(
  workspaceId: string,
  newWorkspaceId: string,
): Promise<void> {
  return invoke("workspace_rename", { workspaceId, newWorkspaceId })
}

// Provider commands
export async function providerList(): Promise<Provider[]> {
  return invoke<Provider[]>("provider_list")
}

export async function providerAdd(
  name: string,
  source?: string,
): Promise<void> {
  return invoke("provider_add", { name, source })
}

export async function providerDelete(name: string): Promise<void> {
  return invoke("provider_delete", { name })
}

export async function providerUse(name: string): Promise<void> {
  return invoke("provider_use", { name })
}

export async function providerInit(name: string): Promise<void> {
  return invoke("provider_init", { name })
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
  return invoke<string>("machine_status", { id })
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

// Analytics
export function analyticsTrack(
  name: string,
  properties?: Record<string, unknown>,
): void {
  invoke("analytics_track", { name, properties }).catch(() => {})
}
