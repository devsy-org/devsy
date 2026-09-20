// Package machinediagnostics implements the portable, user-readable
// diagnostics contract produced by the machine inactivity daemon.
package machinediagnostics

import "time"

const (
	SchemaVersion               = 1
	MaxRemoteEventBytes         = 5 * 1024 * 1024
	MaxSegmentBytes             = 512 * 1024
	MaxEventMessageBytes        = 4 * 1024
	MaxReadEvents               = 1000
	DefaultReadEvents           = 100
	diagnosticsPermissionDenied = "diagnostics_permission_denied"
	diagnosticsCorrupt          = "diagnostics_corrupt"
)

type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

type EventType string

const (
	EventDaemonStarted       EventType = "daemon.started"
	EventDaemonReady         EventType = "daemon.ready"
	EventDaemonStopping      EventType = "daemon.stopping"
	EventWorkspaceDiscovered EventType = "workspace.discovered"
	EventWorkspaceRemoved    EventType = "workspace.removed"
	EventWorkspaceConfigErr  EventType = "workspace.config_error"
	EventIdleDeadline        EventType = "workspace.idle_deadline_reached"
	EventShutdownStarted     EventType = "shutdown.started"
	EventShutdownSucceeded   EventType = "shutdown.succeeded"
	EventShutdownFailed      EventType = "shutdown.failed"
	EventPatrolFailed        EventType = "patrol.failed"
)

type Event struct {
	SchemaVersion int       `json:"schemaVersion"`
	SessionID     string    `json:"sessionId"`
	Sequence      uint64    `json:"sequence"`
	Timestamp     time.Time `json:"timestamp"`
	Level         Level     `json:"level"`
	Type          EventType `json:"type"`
	WorkspaceID   string    `json:"workspaceId,omitempty"`
	Message       string    `json:"message"`
	ErrorCode     string    `json:"errorCode,omitempty"`
}

type DaemonState string

const (
	DaemonStarting DaemonState = "starting"
	DaemonRunning  DaemonState = "running"
	DaemonStopping DaemonState = "stopping"
)

type DaemonHealth string

const (
	DaemonHealthy  DaemonHealth = "healthy"
	DaemonDegraded DaemonHealth = "degraded"
)

type WorkspaceEvaluationState string

const (
	WorkspaceActive        WorkspaceEvaluationState = "active"
	WorkspaceIdleDue       WorkspaceEvaluationState = "idle_due"
	WorkspaceBusy          WorkspaceEvaluationState = "busy"
	WorkspaceNotConfigured WorkspaceEvaluationState = "not_configured"
	WorkspaceInvalidConfig WorkspaceEvaluationState = "invalid_config"
	WorkspaceNotRunning    WorkspaceEvaluationState = "not_running"
)

type DiagnosticError struct {
	Code      string    `json:"code"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}
type WorkspaceStatus struct {
	ID                    string                   `json:"id"`
	State                 WorkspaceEvaluationState `json:"state"`
	LastActivityAt        *time.Time               `json:"lastActivityAt,omitempty"`
	Timeout               string                   `json:"inactivityTimeout,omitempty"`
	IdleDeadlineAt        *time.Time               `json:"idleDeadlineAt,omitempty"`
	Busy                  bool                     `json:"busy"`
	ShutdownActionEnabled bool                     `json:"shutdownActionEnabled"`
	BlocksMachineShutdown bool                     `json:"blocksMachineShutdown"`
	BlockerReason         string                   `json:"blockerReason,omitempty"`
}
type ShutdownCandidate struct {
	WorkspaceID string     `json:"workspaceId"`
	EligibleAt  *time.Time `json:"eligibleAt,omitempty"`
}
type Status struct {
	SchemaVersion     int                `json:"schemaVersion"`
	SessionID         string             `json:"sessionId"`
	StartedAt         time.Time          `json:"startedAt"`
	UpdatedAt         time.Time          `json:"updatedAt"`
	State             DaemonState        `json:"state"`
	Health            DaemonHealth       `json:"health"`
	PatrolInterval    string             `json:"patrolInterval"`
	LastPatrolAt      *time.Time         `json:"lastPatrolAt,omitempty"`
	LastSuccessAt     *time.Time         `json:"lastSuccessfulPatrolAt,omitempty"`
	LastError         *DiagnosticError   `json:"lastError,omitempty"`
	WorkspaceCount    int                `json:"workspaceCount"`
	Workspaces        []WorkspaceStatus  `json:"workspaces,omitempty"`
	ShutdownCandidate *ShutdownCandidate `json:"shutdownCandidate,omitempty"`
	LatestSequence    uint64             `json:"latestSequence"`
}
type Availability string

const (
	AvailabilityAvailable        Availability = "available"
	AvailabilityNotInitialized   Availability = "not_initialized"
	AvailabilityPermissionDenied Availability = "permission_denied"
	AvailabilityCorrupt          Availability = "corrupt"
	AvailabilityUnavailable      Availability = "unavailable"
)

type Freshness string

const (
	FreshnessFresh   Freshness = "fresh"
	FreshnessStale   Freshness = "stale"
	FreshnessUnknown Freshness = "unknown"
)

type CursorState string

const (
	CursorOK    CursorState = "ok"
	CursorGap   CursorState = "gap"
	CursorReset CursorState = "reset"
	CursorNone  CursorState = "none"
)

type CursorInfo struct {
	Next   string      `json:"next,omitempty"`
	State  CursorState `json:"state"`
	Reason string      `json:"reason,omitempty"`
}
type ReadResponse struct {
	SchemaVersion int              `json:"schemaVersion"`
	Availability  Availability     `json:"availability"`
	ObservedAt    time.Time        `json:"observedAt"`
	Freshness     Freshness        `json:"freshness"`
	Status        *Status          `json:"status,omitempty"`
	Events        []Event          `json:"events,omitempty"`
	Cursor        CursorInfo       `json:"cursor"`
	Error         *DiagnosticError `json:"error,omitempty"`
}
type ReaderIdentity struct {
	UID int
	GID int
}
