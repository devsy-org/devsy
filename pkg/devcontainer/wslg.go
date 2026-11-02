package devcontainer

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"runtime"
	"strings"
	"unicode/utf8"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/wslg"
)

const wslgSnapshotLabel = "devsy.wslg.snapshot"

// The local context is the authority. Neither agent workspace JSON nor a
// downloaded provider can opt in, and cleanup never calls this method.
func (r *runner) prepareWSLg(ctx context.Context, options provider.CLIOptions) error {
	r.wslgSnapshot = nil
	if r.workspaceConfig == nil || r.workspaceConfig.Workspace == nil {
		return nil
	}
	cfg, err := pkgconfig.LoadConfig(r.workspaceConfig.Workspace.Context, "")
	if err != nil {
		return fmt.Errorf("read local WSLg authorization: %w", err)
	}
	if !cfg.ContextOptionBool(pkgconfig.ContextOptionWSLgEnabled) {
		return nil
	}
	if err := r.validateWSLgSupport(options); err != nil {
		return err
	}
	target := wslg.Target{
		Distro: cfg.ContextOption(pkgconfig.ContextOptionWSLgDistro),
		User:   cfg.ContextOption(pkgconfig.ContextOptionWSLgUser),
	}
	if err := wslg.ValidateTarget(target); err != nil {
		return err
	}
	if err := r.checkWSLgCollisions(options); err != nil {
		return err
	}
	r.wslgSnapshot, err = wslg.Discover(ctx, target)
	return err
}

func (r *runner) validateWSLgSupport(options provider.CLIOptions) error {
	helper, ok := r.driver.(driver.DockerHelperProvider)
	if runtime.GOOS != goosWindows || !ok || !r.localDockerWSLgScope(options) {
		return wslg.ErrUnsupported
	}
	dockerHelper, err := helper.DockerHelper()
	if err != nil {
		return err
	}
	if dockerHelper.GetRuntime().Name() != docker.RuntimeDocker {
		return wslg.ErrUnsupported
	}
	return nil
}

func (r *runner) localDockerWSLgScope(options provider.CLIOptions) bool {
	return r.workspaceConfig.Agent.Local == pkgconfig.BoolTrue && !options.Platform.Enabled &&
		(r.workspaceConfig.Agent.Driver == "" || r.workspaceConfig.Agent.Driver == provider.DockerDriver)
}

func (r *runner) checkWSLgCollisions(options provider.CLIOptions) error {
	return wslg.CheckEnvironmentSources(map[string][]string{
		"process environment":         os.Environ(),
		"--init-env":                  options.InitEnv,
		"provider Docker environment": wslg.Pairs(r.workspaceConfig.Agent.Docker.Env),
	}, runtime.GOOS == goosWindows)
}

type wslgReplay struct {
	Target      wslg.Target       `json:"target"`
	Environment map[string]string `json:"environment"`
}

func (r *runner) wslgReplay() *wslgReplay {
	if r.wslgSnapshot == nil {
		return nil
	}
	return &wslgReplay{Target: r.wslgSnapshot.Target(), Environment: r.wslgSnapshot.Environment()}
}

func validateWSLgReplay(replay *wslgReplay) error {
	if replay == nil {
		return fmt.Errorf("missing persisted WSLg metadata")
	}
	if err := wslg.ValidateTarget(replay.Target); err != nil {
		return err
	}
	if replay.Environment[wslg.Display] == "" && replay.Environment[wslg.WaylandDisplay] == "" {
		return fmt.Errorf("persisted WSLg metadata has no usable display")
	}
	return wslg.ValidateEnvironment(replay.Environment)
}

func readWSLgReplay(details *config.ContainerDetails) (*wslgReplay, error) {
	value := details.Config.Labels[wslgSnapshotLabel]
	if value == "" {
		return nil, nil
	}
	replay := &wslgReplay{}
	if err := decodeWSLgMetadata(value, replay); err != nil {
		return nil, fmt.Errorf("invalid persisted WSLg metadata")
	}
	if err := validateWSLgReplay(replay); err != nil {
		return nil, err
	}
	return replay, nil
}

func decodeWSLgMetadata(value string, result any) error {
	if !utf8.ValidString(value) || !json.Valid([]byte(value)) {
		return fmt.Errorf("invalid persisted WSLg/Compose metadata")
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		return fmt.Errorf("invalid persisted WSLg/Compose metadata")
	}
	return nil
}

func (r *runner) checkWSLgReuse(details *config.ContainerDetails, recreate bool) error {
	if details == nil || recreate {
		return nil
	}
	previous, err := readWSLgReplay(details)
	if err != nil {
		return err
	}
	current := r.wslgReplay()
	if previous == nil && current == nil {
		return nil
	}
	if replayMatches(previous, current) &&
		details.State.Status == config.ContainerStatusRunning {
		return nil
	}
	return fmt.Errorf(
		"WSLg configuration changed or its container is stopped; " +
			"rerun with --recreate to refresh display environment and socket mounts",
	)
}

// A stopped container keeps display values and socket mounts from creation.
func checkWSLgResume(details *config.ContainerDetails) error {
	replay, err := readWSLgReplay(details)
	if err != nil {
		return err
	}
	if replay == nil {
		return nil
	}
	return fmt.Errorf(
		"WSLg workspace container is stopped; rerun workspace up with --recreate " +
			"to refresh display environment and socket mounts before connecting",
	)
}

func replayMatches(previous, current *wslgReplay) bool {
	return previous != nil && current != nil && previous.Target == current.Target &&
		maps.Equal(previous.Environment, current.Environment)
}

func (r *runner) wslgLabels() (map[string]string, error) {
	replay := r.wslgReplay()
	if replay == nil {
		return nil, nil
	}
	value, err := json.Marshal(replay)
	if err != nil {
		return nil, err
	}
	return map[string]string{wslgSnapshotLabel: string(value)}, nil
}
