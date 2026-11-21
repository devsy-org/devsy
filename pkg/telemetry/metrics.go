package telemetry

import (
	"context"
	"errors"
	"maps"
	"sort"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/status"
)

type ProviderType string

const (
	ProviderTypeBuiltin       ProviderType = "builtin"
	ProviderTypeMachineBinary ProviderType = "machine_binary"
	ProviderTypeScriptProxy   ProviderType = "script_proxy"
	ProviderTypeExternalSDK   ProviderType = "external_sdk"
	ProviderTypeUnknown       ProviderType = "unknown"
)

type ProviderOperation struct {
	ProviderType ProviderType
	Operation    string
	Duration     time.Duration
	ErrorCode    string
}

type ProviderMetric struct {
	ProviderType     ProviderType `json:"provider_type"`
	Operation        string       `json:"operation"`
	Count            int64        `json:"count"`
	DurationMs       int64        `json:"duration_ms"`
	RuntimeErrorCode string       `json:"runtime_error_code,omitempty"`
	duration         time.Duration
}

type MetricsSnapshot struct {
	DurationMs         int64            `json:"duration_ms"`
	PhaseDurationsMs   map[string]int64 `json:"phase_durations_ms,omitempty"`
	FailedPhase        string           `json:"failed_phase,omitempty"`
	ErrorCode          string           `json:"error_code,omitempty"`
	ProviderOperations []ProviderMetric `json:"provider_operations,omitempty"`
}

type metricOperation struct {
	parent   string
	phase    string
	duration time.Duration
	cause    error
	terminal bool
}

type MetricsReporter struct {
	mu         sync.Mutex
	started    time.Time
	operations map[string]*metricOperation
	providers  map[providerKey]*ProviderMetric
	finished   bool
	snapshot   MetricsSnapshot
}

type providerKey struct {
	kind      ProviderType
	operation string
	code      string
}

type metricsContextKey struct{}

func NewMetricsReporter() *MetricsReporter {
	return &MetricsReporter{
		started:    time.Now(),
		operations: make(map[string]*metricOperation),
		providers:  make(map[providerKey]*ProviderMetric),
	}
}

func WithMetrics(ctx context.Context, metrics *MetricsReporter) context.Context {
	ctx = context.WithValue(ctx, metricsContextKey{}, metrics)
	if metrics != nil {
		ctx = status.WithObserver(ctx, metrics)
	}
	return ctx
}

func MetricsFromContext(ctx context.Context) *MetricsReporter {
	if ctx == nil {
		return nil
	}
	metrics, _ := ctx.Value(metricsContextKey{}).(*MetricsReporter)
	return metrics
}

func phaseCategory(phase status.Phase) string {
	switch phase {
	case status.PhaseCloningRepository,
		status.PhaseResolvingConfig,
		status.PhaseInitializeCommand,
		status.PhasePreparingDevContainer,
		status.PhaseConfiguringWorkspace:
		return "prepare"
	case status.PhaseStoppingWorkspace,
		status.PhaseDeletingWorkspace,
		status.PhaseRebuildingWorkspace,
		status.PhaseResettingWorkspace,
		status.PhaseImportingWorkspace,
		status.PhaseMachineCreate,
		status.PhaseMachineStart,
		status.PhaseMachineStop,
		status.PhaseMachineDelete,
		status.PhaseMachineStatus:
		return "machine"
	case status.PhaseInstallingProvider,
		status.PhaseResolvingOptions,
		status.PhaseRunningInit,
		status.PhaseRunningCommand:
		return "provider"
	case status.PhaseInjectingAgent:
		return "agent"
	case status.PhaseBuildingImage:
		return "build"
	default:
		return finalPhaseCategory(phase)
	}
}

func finalPhaseCategory(phase status.Phase) string {
	switch phase {
	case status.PhaseStartingContainer, status.PhaseRunningLifecycleHook, status.PhaseWaitingFor:
		return "container"
	case status.PhaseConfiguringSSH, status.PhaseStartingSSHTunnel, status.PhaseLaunchingIDE:
		return "ide"
	case status.PhaseReady:
		return "ready"
	default:
		return ""
	}
}

func (m *MetricsReporter) Report(event status.Event) {
	if event.OperationID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.finished {
		return
	}
	op := m.operations[event.OperationID]
	if op == nil {
		if len(m.operations) >= 4096 {
			return
		}
		op = &metricOperation{parent: event.ParentOperationID, phase: phaseCategory(event.Phase)}
		m.operations[event.OperationID] = op
	}
	recordTerminalEvent(op, event)
}

func recordTerminalEvent(op *metricOperation, event status.Event) {
	if event.State == status.StateStarted || op.terminal {
		return
	}
	if event.State != status.StateSucceeded && event.State != status.StateFailed &&
		event.State != status.StateSkipped {
		return
	}
	op.terminal = true
	op.duration = event.Duration
	if event.State == status.StateFailed {
		op.cause = event.Cause
	}
}

func (m *MetricsReporter) Finish(err error) MetricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.finished {
		m.finalize(err)
	}
	result := m.snapshot
	result.PhaseDurationsMs = maps.Clone(result.PhaseDurationsMs)
	result.ProviderOperations = append([]ProviderMetric(nil), result.ProviderOperations...)
	return result
}

func (m *MetricsReporter) finalize(err error) {
	m.finished = true
	snapshot := MetricsSnapshot{
		DurationMs:       time.Since(m.started).Milliseconds(),
		PhaseDurationsMs: make(map[string]int64),
	}
	if err != nil {
		snapshot.ErrorCode = safeErrorCode(string(clierr.Classify(err).Code))
	}
	m.collectPhases(&snapshot, primaryError(err))
	for _, metric := range m.providers {
		snapshot.ProviderOperations = append(snapshot.ProviderOperations, *metric)
	}
	sort.Slice(snapshot.ProviderOperations, func(i, j int) bool {
		a, b := snapshot.ProviderOperations[i], snapshot.ProviderOperations[j]
		if a.ProviderType != b.ProviderType {
			return a.ProviderType < b.ProviderType
		}
		if a.Operation != b.Operation {
			return a.Operation < b.Operation
		}
		return a.RuntimeErrorCode < b.RuntimeErrorCode
	})
	m.snapshot = snapshot
	m.operations = nil
	m.providers = nil
}

func (m *MetricsReporter) collectPhases(snapshot *MetricsSnapshot, primary error) {
	durations := make(map[string]time.Duration)
	attribution := phaseAttribution{depth: -1, primary: primary, code: snapshot.ErrorCode}
	for _, op := range m.operations {
		if !op.terminal || op.phase == "" {
			continue
		}
		depth, covered, recovered := m.operationAncestors(op)
		if !covered {
			durations[op.phase] += op.duration
		}
		attribution.consider(op, depth, recovered)
	}
	for phase, duration := range durations {
		snapshot.PhaseDurationsMs[phase] = duration.Milliseconds()
	}
	if attribution.ambiguous {
		snapshot.FailedPhase = ""
	} else {
		snapshot.FailedPhase = attribution.phase
	}
}

type phaseAttribution struct {
	primary   error
	code      string
	depth     int
	phase     string
	ambiguous bool
}

func (a *phaseAttribution) consider(op *metricOperation, depth int, recovered bool) {
	if !a.matches(op, recovered) {
		return
	}
	if depth > a.depth {
		a.depth, a.phase, a.ambiguous = depth, op.phase, false
	} else if depth == a.depth && a.phase != op.phase {
		a.ambiguous = true
	}
}

func (a *phaseAttribution) matches(op *metricOperation, recovered bool) bool {
	return a.primary != nil && a.code != "PANIC" && !recovered && op.cause != nil &&
		errors.Is(a.primary, op.cause)
}

func (m *MetricsReporter) operationAncestors(
	op *metricOperation,
) (depth int, covered, recovered bool) {
	for parent := op.parent; parent != "" && depth < 4096; {
		p := m.operations[parent]
		if p == nil {
			break
		}
		depth++
		if p.phase == op.phase {
			covered = true
		}
		if p.terminal && p.cause == nil {
			recovered = true
		}
		parent = p.parent
	}
	return depth, covered, recovered
}

func primaryError(err error) error {
	for current := err; current != nil; current = errors.Unwrap(current) {
		if joined, ok := current.(interface{ Unwrap() []error }); ok {
			for _, cause := range joined.Unwrap() {
				if cause != nil {
					return primaryError(cause)
				}
			}
			return nil
		}
	}
	return err
}

func safeErrorCode(code string) string {
	switch code {
	case "RATE_LIMITED",
		"PANIC",
		"UNKNOWN",
		"BUILD_FAILED_RECOVERABLE",
		"docker_daemon_unreachable",
		"canceled",
		"deadline_exceeded",
		"unlock_required",
		"unlock_failed",
		"secret_backend_unavailable",
		"secret_store_corrupt",
		"secret_not_found",
		"ssh_auth_failed",
		"ssh_host_key_failed",
		"ssh_unreachable",
		"ssh_command_failed":
		return code
	default:
		return "UNKNOWN"
	}
}

func safeRuntimeErrorCode(code string) string {
	switch code {
	case "unknown",
		"not_found",
		"already_exists",
		"invalid_argument",
		"unsupported",
		"unavailable",
		"failed_precondition",
		"permission_denied",
		"timeout",
		"cancelled",
		"runtime_failure",
		"incompatible_version",
		"aws_credentials_expired",
		"aws_quota_exceeded",
		"aws_ssm_unreachable",
		"aws_subnet_misconfigured",
		"canceled",
		"deadline_exceeded",
		"ssh_auth_failed",
		"ssh_host_key_failed",
		"ssh_unreachable",
		"ssh_command_failed":
		return code
	default:
		return "unknown"
	}
}

func RecordProviderOperation(ctx context.Context, operation ProviderOperation) {
	m := MetricsFromContext(ctx)
	if m == nil {
		return
	}
	operation = normalizeProviderOperation(operation)
	if operation.Duration < 0 {
		operation.Duration = 0
	}
	key := providerKey{operation.ProviderType, operation.Operation, operation.ErrorCode}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.finished {
		return
	}
	metric := m.providers[key]
	if metric == nil {
		metric = &ProviderMetric{
			ProviderType:     key.kind,
			Operation:        key.operation,
			RuntimeErrorCode: key.code,
		}
		m.providers[key] = metric
	}
	metric.Count++
	metric.duration += operation.Duration
	metric.DurationMs = metric.duration.Milliseconds()
}

func normalizeProviderOperation(operation ProviderOperation) ProviderOperation {
	switch operation.ProviderType {
	case ProviderTypeBuiltin,
		ProviderTypeMachineBinary,
		ProviderTypeScriptProxy,
		ProviderTypeExternalSDK:
	default:
		operation.ProviderType = ProviderTypeUnknown
	}
	switch operation.Operation {
	case "Info",
		"Preflight",
		"ProvisioningPreflight",
		"ReusePreflight",
		"Find",
		"TargetArchitecture",
		"RunImage",
		"Start",
		"Stop",
		"Delete",
		"Exec",
		"Logs",
		"create",
		"command",
		"start",
		"stop",
		"delete",
		"status",
		"describe":
	default:
		operation.Operation = metricOther
	}
	if operation.ErrorCode != "" {
		operation.ErrorCode = safeRuntimeErrorCode(operation.ErrorCode)
	}
	return operation
}
