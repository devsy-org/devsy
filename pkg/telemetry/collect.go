package telemetry

import (
	"context"
	"maps"
	"os"
	"runtime"
	"time"

	devsyclient "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/telemetry/analytics"
	"github.com/devsy-org/devsy/pkg/version"
	"github.com/moby/term"
	"github.com/spf13/cobra"
)

// Set on cobra commands the desktop polls frequently so their invocations
// don't drown out meaningful CLI activity.
const AnnotationSkipInUI = "telemetry.skip-in-ui"

// SkipInUIAnnotation returns the cobra Annotations map that flags a command
// as one the desktop polls frequently.
func SkipInUIAnnotation() map[string]string {
	return map[string]string{AnnotationSkipInUI: config.BoolTrue}
}

type ErrorSeverityType string

const (
	WarningSeverity ErrorSeverityType = "warning"
	ErrorSeverity   ErrorSeverityType = "error"
	FatalSeverity   ErrorSeverityType = "fatal"
	PanicSeverity   ErrorSeverityType = "panic"
)

type CLICollector interface {
	RecordCLI(err error)
	RecordWorkspaceGauge(count int)
	SetClient(client devsyclient.BaseWorkspaceClient)
	Flush()
}

type ctxKey struct{}

func WithCollector(ctx context.Context, c CLICollector) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// Returns a noop collector when none is present so callers can use it unguarded.
func FromContext(ctx context.Context) CLICollector {
	if c, ok := ctx.Value(ctxKey{}).(CLICollector); ok && c != nil {
		return c
	}
	return &noopCollector{}
}

// Call before LoadConfig so config-load failures still get recorded.
// Always returns a non-nil collector.
func BootstrapCLI(cmd *cobra.Command) CLICollector {
	if version.GetVersion() == version.DevVersion ||
		os.Getenv(config.EnvDisableTelemetry) == config.BoolTrue {
		return &noopCollector{}
	}

	isUI := os.Getenv(config.EnvUI) == config.BoolTrue
	isProRunner := os.Getenv(config.EnvProRunner) == config.BoolTrue
	if !isUI && !isProRunner && isCIEnvironment() {
		log.Debug("telemetry: disabled in CI environment")
		return &noopCollector{}
	}

	collector, err := newCLICollector(cmd)
	if err != nil {
		log.Infof("telemetry: %s", err.Error())
		return &noopCollector{}
	}
	return collector
}

// Swaps current for a noop collector if the user has opted out via context.
func ApplyCLIConfig(devsyConfig *config.Config, current CLICollector) CLICollector {
	if devsyConfig == nil {
		return current
	}
	if devsyConfig.ContextOption(config.ContextOptionTelemetry) == config.BoolFalse {
		return &noopCollector{}
	}
	return current
}

func newCLICollector(cmd *cobra.Command) (*cliCollector, error) {
	defaultCollector := &cliCollector{
		analyticsClient: analytics.NewClient(),
		cmd:             cmd,
	}

	return defaultCollector, nil
}

type cliCollector struct {
	analyticsClient analytics.Client
	cmd             *cobra.Command
	client          devsyclient.BaseWorkspaceClient
}

func (d *cliCollector) SetClient(client devsyclient.BaseWorkspaceClient) {
	d.client = client
}

func (d *cliCollector) Flush() {
	d.analyticsClient.Flush()
}

func (d *cliCollector) RecordCLI(err error) {
	if d.cmd == nil {
		log.Debug("no command found, skipping")
		return
	}
	cmd := d.cmd.CommandPath()
	isUI := os.Getenv(config.EnvUI) == config.BoolTrue
	if isUI && d.cmd.Annotations[AnnotationSkipInUI] == config.BoolTrue {
		return
	}

	isCI := false
	if !isUI {
		isCI = isCIEnvironment()
	}

	isInteractive := false
	if !isUI {
		isInteractive = isInteractiveShell()
	}

	timezone, _ := time.Now().Zone()
	eventProperties := d.buildEventProperties(eventPropertiesParams{
		cmd:           cmd,
		isUI:          isUI,
		isCI:          isCI,
		isInteractive: isInteractive,
		err:           err,
	})
	userProperties := map[string]any{
		"os_name":  runtime.GOOS,
		"os_arch":  runtime.GOARCH,
		"timezone": timezone,
	}

	eventType := config.BinaryName + "_cli"
	if os.Getenv(config.EnvProRunner) == config.BoolTrue {
		eventType = config.BinaryName + "_cli_runner"
	}

	d.recordEvent(eventType, eventProperties, userProperties)
}

type eventPropertiesParams struct {
	cmd           string
	isUI          bool
	isCI          bool
	isInteractive bool
	err           error
}

func (d *cliCollector) RecordWorkspaceGauge(count int) {
	timezone, _ := time.Now().Zone()
	d.recordEvent(
		config.BinaryName+"_workspace_count",
		map[string]any{
			"count":   count,
			"version": version.GetVersion(),
			"desktop": os.Getenv(config.EnvUI) == config.BoolTrue,
		},
		map[string]any{
			"os_name":  runtime.GOOS,
			"os_arch":  runtime.GOARCH,
			"timezone": timezone,
		},
	)
}

func (d *cliCollector) buildEventProperties(p eventPropertiesParams) map[string]any {
	eventProperties := map[string]any{
		"command":        p.cmd,
		"version":        version.GetVersion(),
		"desktop":        p.isUI,
		"is_ci":          p.isCI,
		"is_interactive": p.isInteractive,
	}
	if d.client != nil {
		eventProperties["provider"] = d.client.Provider()

		if workspaceConfig := d.client.WorkspaceConfig(); workspaceConfig != nil {
			eventProperties["source_type"] = workspaceConfig.Source.Type()
			eventProperties["ide"] = workspaceConfig.IDE.Name
		}
	}
	// Raw err.Error() strings can leak paths, hostnames, tokens.
	if p.err != nil {
		eventProperties["error_code"] = string(clierr.Classify(p.err).Code)
	}
	return eventProperties
}

func (d *cliCollector) recordEvent(
	eventType string,
	eventProperties, userProperties map[string]any,
) {
	machineID := GetMachineID()
	timestamp := time.Now().Unix()

	eventPayload := map[string]any{}
	maps.Copy(eventPayload, eventProperties)
	eventPayload[analytics.KeyType] = eventType
	eventPayload[analytics.KeyMachineID] = machineID
	eventPayload[analytics.KeyTimestamp] = timestamp

	if wsUID := os.Getenv(config.EnvWorkspaceUID); wsUID != "" {
		eventPayload["in_container"] = true
		eventPayload["workspace_id"] = hashScopedID(machineID, wsUID)
	}

	userPayload := map[string]any{}
	maps.Copy(userPayload, userProperties)
	userPayload[analytics.KeyMachineID] = machineID
	userPayload[analytics.KeyTimestamp] = timestamp

	d.analyticsClient.RecordEvent(analytics.Event{
		"event": eventPayload,
		"user":  userPayload,
	})
}

// isCIEnvironment looks up a couple of well-known CI env vars.
func isCIEnvironment() bool {
	ciIndicators := []string{
		"CI",                     // Generic CI variable
		"TRAVIS",                 // Travis CI
		"GITHUB_ACTIONS",         // GitHub Actions
		"GITLAB_CI",              // GitLab CI
		"CIRCLECI",               // CircleCI
		"TEAMCITY_VERSION",       // TeamCity
		"BITBUCKET_BUILD_NUMBER", // Bitbucket
	}

	for _, key := range ciIndicators {
		if _, exists := os.LookupEnv(key); exists {
			return true
		}
	}
	return false
}

// isInteractiveShell checks if the current shell is in interactive mode or not.
// Can be combined with `isCi` to narrow down usage.
func isInteractiveShell() bool {
	return term.IsTerminal(os.Stdin.Fd())
}
