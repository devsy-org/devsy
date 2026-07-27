package microsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/image"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

type cliClient struct{}

var _ sandboxClient = cliClient{}

const (
	cmdCreate = "create"
	flagName  = "--name"
	flagEnv   = "--env"
)

func (cliClient) EnsureInstalled(_ context.Context) error {
	bin := msbBinary()
	if filepath.IsAbs(bin) {
		if _, err := os.Stat(bin); err == nil {
			return nil
		}
	} else if _, err := exec.LookPath(bin); err == nil {
		return nil
	}
	return fmt.Errorf(
		"microsandbox runtime (msb) not found; install it from https://install.microsandbox.dev",
	)
}

func (cliClient) EnsureImage(ctx context.Context, imageRef string) error {
	if dockerImageExists(ctx, imageRef) {
		return loadFromDocker(ctx, imageRef)
	}
	// #nosec G204 -- args are a resolved binary path and a validated image ref
	out, err := exec.CommandContext(ctx, msbBinary(), "pull", imageRef).CombinedOutput()
	if err == nil {
		return nil
	}
	if loadErr := loadViaRegistry(ctx, imageRef); loadErr != nil {
		return fmt.Errorf("msb pull %s: %s: %w; registry fallback: %v", imageRef, out, err, loadErr)
	}
	return nil
}

func (c cliClient) Create(ctx context.Context, sandbox string, spec sandboxSpec) error {
	if err := c.ensureVolumes(ctx, spec.Mounts); err != nil {
		return err
	}
	return msbRun(ctx, createArgs(sandbox, spec)...)
}

func (cliClient) Find(ctx context.Context, sandbox string) (*sandboxInfo, error) {
	// #nosec G204 -- args are a resolved binary path and a derived sandbox name
	out, err := exec.CommandContext(ctx, msbBinary(), "inspect", sandbox, "--format", "json").
		Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("inspect microsandbox VM %q: %w", sandbox, ctx.Err())
		}
		// A genuine "not found" means the sandbox is absent (nil, nil). Any other
		// failure (permission, crash, bad invocation) is a real error to surface,
		// so callers do not mistake it for an absent sandbox.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && strings.Contains(string(exitErr.Stderr), "not found") {
			return nil, nil
		}
		return nil, fmt.Errorf("inspect microsandbox VM %q: %w", sandbox, err)
	}
	var raw struct {
		Name         string `json:"name"`
		Status       string `json:"status"`
		CreatedAt    string `json:"created_at"`
		ActiveConfig struct {
			Labels map[string]string `json:"labels"`
		} `json:"active_config"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse microsandbox inspect output: %w", err)
	}
	created, _ := time.Parse(time.RFC3339Nano, raw.CreatedAt)
	return &sandboxInfo{
		Name:      raw.Name,
		Running:   strings.EqualFold(raw.Status, "running"),
		CreatedAt: created,
		Labels:    raw.ActiveConfig.Labels,
	}, nil
}

func (cliClient) Start(ctx context.Context, sandbox string) error {
	return msbRun(ctx, "start", sandbox)
}

func (cliClient) Stop(ctx context.Context, sandbox string) error {
	return msbRun(ctx, "stop", sandbox)
}

func (cliClient) Remove(ctx context.Context, sandbox string) error {
	return msbRun(ctx, "remove", sandbox)
}

// Exec uses --stream (byte-faithful stdio, no PTY) because the agent binary
// injection and the SSH-over-stdio tunnel require it; a PTY stalls the tunnel.
func (cliClient) Exec(ctx context.Context, sandbox string, req execRequest) error {
	args := []string{"exec", "--stream"}
	if req.User != "" {
		args = append(args, "--user", req.User)
	}
	args = append(args, sandbox, "--")
	if len(req.Argv) > 0 {
		args = append(args, req.Argv...)
	} else {
		args = append(args, "/bin/sh", "-c", req.Command)
	}
	// #nosec G204 -- args are a resolved binary path plus the caller's command
	cmd := exec.CommandContext(ctx, msbBinary(), args...)
	cmd.Stdin = req.Stdin
	cmd.Stdout = req.Stdout
	cmd.Stderr = req.Stderr
	return cmd.Run()
}

func (cliClient) Logs(ctx context.Context, sandbox string, w io.Writer) error {
	// #nosec G204 -- args are a resolved binary path and a derived sandbox name
	cmd := exec.CommandContext(ctx, msbBinary(), "logs", sandbox)
	cmd.Stdout = w
	cmd.Stderr = w
	return cmd.Run()
}

func (cliClient) ensureVolumes(ctx context.Context, mounts []volumeMount) error {
	for _, vol := range namedVolumes(mounts) {
		// #nosec G204 -- args are a resolved binary path and a named-volume name
		out, err := exec.CommandContext(ctx, msbBinary(), "volume", "create", vol).CombinedOutput()
		if err != nil && !strings.Contains(strings.ToLower(string(out)), "already exists") {
			return fmt.Errorf("create microsandbox volume %q: %s: %w", vol, out, err)
		}
	}
	return nil
}

func createArgs(sandbox string, spec sandboxSpec) []string {
	args := []string{cmdCreate, flagName, sandbox}
	args = append(args, runtimeArgs(spec)...)
	args = append(args, resourceArgs(spec)...)
	args = append(args, mountArgs(spec.Mounts)...)
	return append(args, spec.Image)
}

func runtimeArgs(spec sandboxSpec) []string {
	var args []string
	if len(spec.Entrypoint) > 0 {
		args = append(args, "--entrypoint", strings.Join(spec.Entrypoint, " "))
	}
	for k, v := range spec.Env {
		args = append(args, flagEnv, k+"="+v)
	}
	for k, v := range spec.Labels {
		args = append(args, "--label", k+"="+v)
	}
	if spec.IdleTimeout > 0 {
		args = append(args, "--idle-timeout", spec.IdleTimeout.String())
	}
	if spec.BlockEgress {
		args = append(args, "--net-default-egress", "deny")
	}
	return args
}

func resourceArgs(spec sandboxSpec) []string {
	var args []string
	if spec.Memory > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dM", spec.Memory))
	}
	if spec.MaxMemory > 0 {
		args = append(args, "--max-memory", fmt.Sprintf("%dM", spec.MaxMemory))
	}
	if spec.CPUs > 0 {
		args = append(args, "--cpus", strconv.Itoa(int(spec.CPUs)))
	}
	if spec.MaxCPUs > 0 {
		args = append(args, "--max-cpus", strconv.Itoa(int(spec.MaxCPUs)))
	}
	return args
}

func mountArgs(mounts []volumeMount) []string {
	var args []string
	for _, m := range mounts {
		switch {
		case m.Tmpfs:
			args = append(args, "--tmpfs", m.Target)
		case m.Volume != "":
			args = append(args, "--mount-named", m.Volume+":"+m.Target)
		}
	}
	return args
}

func namedVolumes(mounts []volumeMount) []string {
	var names []string
	for _, m := range mounts {
		if !m.Tmpfs && m.Volume != "" {
			names = append(names, m.Volume)
		}
	}
	return names
}

func msbRun(ctx context.Context, args ...string) error {
	// #nosec G204 -- args are a resolved binary path and controlled config values
	out, err := exec.CommandContext(ctx, msbBinary(), args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"msb %s: %s: %w",
			redactArgs(args),
			strings.TrimSpace(string(out)),
			err,
		)
	}
	return nil
}

// redactArgs joins args for an error message while masking --env values, which
// may carry secrets from the devcontainer configuration.
func redactArgs(args []string) string {
	out := make([]string, len(args))
	copy(out, args)
	for i := 0; i+1 < len(out); i++ {
		if out[i] != flagEnv {
			continue
		}
		if k, _, ok := strings.Cut(out[i+1], "="); ok {
			out[i+1] = k + "=***"
		}
	}
	return strings.Join(out, " ")
}

func dockerImageExists(ctx context.Context, image string) bool {
	docker, err := exec.LookPath("docker")
	if err != nil {
		return false
	}
	// #nosec G204 -- docker path is resolved and the image ref is validated
	return exec.CommandContext(ctx, docker, "image", "inspect", image).Run() == nil
}

func loadFromDocker(ctx context.Context, image string) error {
	docker, err := exec.LookPath("docker")
	if err != nil {
		return fmt.Errorf("docker not found to load built image %q: %w", image, err)
	}
	// #nosec G204 -- docker/msb paths are resolved and the image ref is validated
	save := exec.CommandContext(ctx, docker, "save", image)
	// #nosec G204 -- docker/msb paths are resolved and the image ref is validated
	load := exec.CommandContext(ctx, msbBinary(), "load", "-t", image)
	pipe, err := save.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pipe docker save: %w", err)
	}
	load.Stdin = pipe
	var loadErr strings.Builder
	load.Stderr = &loadErr
	if err := load.Start(); err != nil {
		return fmt.Errorf("start msb load: %w", err)
	}
	if err := save.Run(); err != nil {
		// load is still running on the broken pipe; kill and reap it.
		_ = load.Process.Kill()
		_ = load.Wait()
		return fmt.Errorf("docker save %q: %w", image, err)
	}
	if err := load.Wait(); err != nil {
		return fmt.Errorf("msb load %q: %s: %w", image, loadErr.String(), err)
	}
	return nil
}

func loadViaRegistry(ctx context.Context, imageRef string) error {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return fmt.Errorf("parse image reference %q: %w", imageRef, err)
	}
	img, err := image.GetImageForArch(ctx, imageRef, runtime.GOARCH)
	if err != nil {
		return fmt.Errorf("pull image %q: %w", imageRef, err)
	}

	tmp, err := os.CreateTemp("", "devsy-msb-*.tar")
	if err != nil {
		return fmt.Errorf("create image tarball: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	if err := tarball.WriteToFile(tmpPath, ref, img); err != nil {
		return fmt.Errorf("write image tarball: %w", err)
	}

	// #nosec G204 -- args are a resolved binary path and an internally-created file
	load := exec.CommandContext(ctx, msbBinary(), "load", "-i", tmpPath, "-t", imageRef)
	if out, err := load.CombinedOutput(); err != nil {
		return fmt.Errorf("msb load %q: %s: %w", imageRef, out, err)
	}
	return nil
}

func msbBinary() string {
	if p, err := exec.LookPath("msb"); err == nil {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, c := range []string{
			filepath.Join(home, ".local", "bin", "msb"),
			filepath.Join(home, ".microsandbox", "bin", "msb"),
		} {
			if _, statErr := os.Stat(c); statErr == nil {
				return c
			}
		}
	}
	return "msb"
}
