import type {
  CommandProgress,
  Context,
  Machine,
  Provider,
  Workspace,
} from "$lib/types/index.js"
import { listen } from "./bridge.js"
import type { UnlistenFn } from "./types.js"

export interface UpdateStatus {
  state: "checking" | "available" | "not-available" | "downloading" | "downloaded" | "error"
  version?: string
  releaseNotes?: string
  releaseName?: string
  error?: string
}

export const EVENT_NAMES = {
  WORKSPACES_CHANGED: "workspaces-changed",
  PROVIDERS_CHANGED: "providers-changed",
  MACHINES_CHANGED: "machines-changed",
  CONTEXTS_CHANGED: "contexts-changed",
  COMMAND_PROGRESS: "command-progress",
  UPDATE_STATUS: "update-status",
} as const

interface WorkspacesPayload {
  workspaces: Workspace[]
}
interface ProvidersPayload {
  providers: Provider[]
}
interface MachinesPayload {
  machines: Machine[]
}
interface ContextsPayload {
  contexts: Context[]
  activeContext: string
}

export function onWorkspacesChanged(
  callback: (workspaces: Workspace[]) => void,
): Promise<UnlistenFn> {
  return listen<WorkspacesPayload>(EVENT_NAMES.WORKSPACES_CHANGED, (event) => {
    callback(event.payload.workspaces)
  })
}

export function onProvidersChanged(
  callback: (providers: Provider[]) => void,
): Promise<UnlistenFn> {
  return listen<ProvidersPayload>(EVENT_NAMES.PROVIDERS_CHANGED, (event) => {
    callback(event.payload.providers)
  })
}

export function onMachinesChanged(
  callback: (machines: Machine[]) => void,
): Promise<UnlistenFn> {
  return listen<MachinesPayload>(EVENT_NAMES.MACHINES_CHANGED, (event) => {
    callback(event.payload.machines)
  })
}

export function onContextsChanged(
  callback: (contexts: Context[], activeContext: string) => void,
): Promise<UnlistenFn> {
  return listen<ContextsPayload>(EVENT_NAMES.CONTEXTS_CHANGED, (event) => {
    callback(event.payload.contexts, event.payload.activeContext)
  })
}

export function onCommandProgress(
  callback: (progress: CommandProgress) => void,
): Promise<UnlistenFn> {
  return listen<CommandProgress>(EVENT_NAMES.COMMAND_PROGRESS, (event) => {
    callback(event.payload)
  })
}

export function onUpdateStatus(
  callback: (status: UpdateStatus) => void,
): Promise<UnlistenFn> {
  return listen<UpdateStatus>(EVENT_NAMES.UPDATE_STATUS, (event) => {
    callback(event.payload)
  })
}
