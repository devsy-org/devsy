export type DiagnosticAvailability =
  | "available"
  | "machine_stopped"
  | "not_initialized"
  | "permission_denied"
  | "unavailable"
  | "unsupported"
  | "corrupt"

export interface MachineDiagnosticEvent {
  schemaVersion: number
  sessionId: string
  sequence: number
  timestamp: string
  level: "info" | "warn" | "error"
  type: string
  workspaceId?: string
  message: string
  errorCode?: string
}

export interface MachineDaemonStatus {
  sessionId: string
  state: "starting" | "running" | "stopping"
  health: "healthy" | "degraded"
  startedAt: string
  updatedAt: string
  lastPatrolAt?: string
  lastSuccessfulPatrolAt?: string
  workspaceCount: number
  workspaces?: MachineWorkspaceDiagnostics[]
  shutdownCandidate?: { workspaceId: string; eligibleAt?: string }
  lastError?: { code: string; message: string; timestamp: string }
}

export interface MachineWorkspaceDiagnostics {
  id: string
  state: "active" | "idle_due" | "busy" | "not_configured" | "invalid_config" | "not_running"
  lastActivityAt?: string
  inactivityTimeout?: string
  idleDeadlineAt?: string
  busy: boolean
  shutdownActionEnabled: boolean
  blocksMachineShutdown: boolean
  blockerReason?: string
}

export interface MachineDiagnosticsResponse {
  schemaVersion: number
  machine: { id: string; context: string; provider: string; state: string }
  source: { availability: DiagnosticAvailability; freshness: "fresh" | "stale" | "unknown"; errorCode?: string; message?: string }
  daemon?: MachineDaemonStatus
  events?: MachineDiagnosticEvent[]
  cursor: { next?: string; state: "ok" | "gap" | "reset" | "none"; reason?: string }
}

export interface MachineDiagnosticsCache {
  response: MachineDiagnosticsResponse
  events: MachineDiagnosticEvent[]
  cursor?: string
  lastAttemptAt: string
  lastSuccessfulCollectionAt?: string
  historyGap: boolean
  lastCollectionError?: string
}
