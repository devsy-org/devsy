package telemetry

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"time"

	devsyclient "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	cliErrors "github.com/devsy-org/devsy/pkg/errors"
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
	SetClient(client devsyclient.BaseWorkspaceClient)

	// Flush makes sure all events are sent to the backend
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
	eventProperties := map[string]any{
		"command":        cmd,
		"version":        version.GetVersion(),
		"desktop":        isUI,
		"is_ci":          isCI,
		"is_interactive": isInteractive,
	}
	if d.client != nil {
		eventProperties["provider"] = d.client.Provider()

		if d.client.WorkspaceConfig() != nil {
			eventProperties["source_type"] = d.client.WorkspaceConfig().Source.Type()
			eventProperties["ide"] = d.client.WorkspaceConfig().IDE.Name
		}
	}
	userProperties := map[string]any{
		"os_name":  runtime.GOOS,
		"os_arch":  runtime.GOARCH,
		"timezone": timezone,
	}
	// Raw err.Error() strings can leak paths, hostnames, tokens.
	if err != nil {
		eventProperties["error_code"] = string(
			cliErrors.Classify(err, cliErrors.ClassifyContext{}).Code,
		)
	}

	eventType := config.BinaryName + "_cli"
	if os.Getenv(config.EnvProRunner) == config.BoolTrue {
		eventType = config.BinaryName + "_cli_runner"
	}

	// build the event and record
	eventPropertiesRaw, _ := json.Marshal(eventProperties)
	userPropertiesRaw, _ := json.Marshal(userProperties)
	d.analyticsClient.RecordEvent(analytics.Event{
		"event": {
			"type":       eventType,
			"machine_id": GetMachineID(),
			"properties": string(eventPropertiesRaw),
			"timestamp":  time.Now().Unix(),
		},
		"user": {
			"machine_id": GetMachineID(),
			"properties": string(userPropertiesRaw),
			"timestamp":  time.Now().Unix(),
		},
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
