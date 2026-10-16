// Package wslg discovers an explicitly selected WSL user's display connection
// values. It never imports a shell environment or grants access to GUI sockets.
package wslg

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	Display          = "DEVSY_WSLG_DISPLAY"
	WaylandDisplay   = "DEVSY_WSLG_WAYLAND_DISPLAY"
	RuntimeDir       = "DEVSY_WSLG_XDG_RUNTIME_DIR"
	outputLimit      = 4096
	discoveryTimeout = 30 * time.Second
)

var (
	ErrUnsupported = errors.New(
		"WSLg discovery requires native Windows and a local Docker runtime",
	)
	ErrInvalidTarget = errors.New("WSLg discovery requires an explicit non-root WSL distro/user")
	ErrExecutable    = errors.New("WSLg discovery cannot locate the Windows system wsl.exe")
	ErrProtocol      = errors.New(
		"WSLg probe failed; verify the selected distro/user and /usr/bin/printenv",
	)
	ErrUnavailable = errors.New(
		"WSLg has no usable DISPLAY or WAYLAND_DISPLAY for the selected user",
	)
	ErrCollision = errors.New("reserved WSLg environment assignment")
)

type Target struct {
	Distro string `json:"distro"`
	User   string `json:"user"`
}

// Snapshot contains only present keys; an empty string is distinct from absence.
// Accessors return copies so an invocation cannot mutate another's environment.
type Snapshot struct {
	target Target
	env    map[string]string
}

func NewSnapshot(target Target, env map[string]string) (*Snapshot, error) {
	if err := ValidateTarget(target); err != nil {
		return nil, err
	}
	if err := ValidateEnvironment(env); err != nil {
		return nil, err
	}
	return &Snapshot{target: target, env: maps.Clone(env)}, nil
}

func (s *Snapshot) Environment() map[string]string {
	if s == nil {
		return nil
	}
	return maps.Clone(s.env)
}

func (s *Snapshot) Target() Target { return s.target }

func ValidateTarget(target Target) error {
	for _, value := range []string{target.Distro, target.User} {
		if !validTargetName(value) {
			return ErrInvalidTarget
		}
	}
	if target.User == "root" || target.User == "0" {
		return ErrInvalidTarget
	}
	return nil
}

func validTargetName(value string) bool {
	if value == "" || len(value) > 256 || strings.HasPrefix(value, "-") ||
		!utf8.ValidString(value) {
		return false
	}
	for _, ch := range value {
		if unicode.IsControl(ch) || unicode.IsSpace(ch) {
			return false
		}
	}
	return true
}

func Reserved(key string, windows bool) bool {
	if windows {
		key = strings.ToUpper(key)
	}
	return key == Display || key == WaylandDisplay || key == RuntimeDir
}

func ValidateEnvironment(env map[string]string) error {
	for key, value := range env {
		if !Reserved(key, false) || len(value) > outputLimit-1 || !utf8.ValidString(value) ||
			strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("invalid WSLg replay environment")
		}
	}
	return nil
}

func CheckCollisions(source string, env []string, windows bool) error {
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok && Reserved(key, windows) {
			return fmt.Errorf(
				"%w: %s in %s; remove the assignment or disable WSLG_ENABLED",
				ErrCollision,
				key,
				source,
			)
		}
	}
	return nil
}

// CheckEnvironmentSources rejects assignments before provider construction can
// log its effective environment or start a runtime.
func CheckEnvironmentSources(sources map[string][]string, windows bool) error {
	for _, source := range slices.Sorted(maps.Keys(sources)) {
		env := sources[source]
		if err := CheckCollisions(source, env, windows); err != nil {
			return err
		}
		if err := checkComposeEnvFiles(env, windows); err != nil {
			return err
		}
	}
	return nil
}

func checkComposeEnvFiles(env []string, windows bool) error {
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if windows {
			key = strings.ToUpper(key)
		}
		if key == "COMPOSE_ENV_FILES" && value != "" {
			return fmt.Errorf("WSLg discovery does not support COMPOSE_ENV_FILES; " +
				"use project .env files or disable WSLG_ENABLED")
		}
	}
	return nil
}

func Pairs(env map[string]string) []string {
	keys := slices.Sorted(maps.Keys(env))
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+env[key])
	}
	return pairs
}

// Overlay replaces the reserved entries, including Windows case variants, once.
// Ordinary environment precedence remains the caller's responsibility.
func Overlay(base []string, env map[string]string, windows bool) []string {
	out := make([]string, 0, len(base)+len(env))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if !Reserved(key, windows) {
			out = append(out, entry)
		}
	}
	return append(out, Pairs(env)...)
}

type ProbeResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

type Runner interface {
	Run(context.Context, string, []string) (ProbeResult, error)
}

func Discover(ctx context.Context, target Target) (*Snapshot, error) {
	if err := ValidateTarget(target); err != nil {
		return nil, err
	}
	path, err := systemWSLPath()
	if err != nil {
		return nil, err
	}
	return Capture(ctx, target, path, commandRunner{})
}

// Capture exposes the bounded probe for tests; production always resolves the
// trusted executable using the Windows system API in Discover.
func Capture(ctx context.Context, target Target, path string, runner Runner) (*Snapshot, error) {
	if err := ValidateTarget(target); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	env := map[string]string{}
	probe := displayProbe{path: path, target: target, runner: runner}
	for _, key := range []string{"DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR"} {
		value, present, err := probe.value(ctx, key)
		if err != nil {
			return nil, err
		}
		if present {
			env["DEVSY_WSLG_"+key] = value
		}
	}
	if env[Display] == "" && env[WaylandDisplay] == "" {
		return nil, ErrUnavailable
	}
	return NewSnapshot(target, env)
}

type displayProbe struct {
	path   string
	target Target
	runner Runner
}

func (p displayProbe) value(ctx context.Context, key string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	result, err := p.runner.Run(
		ctx,
		p.path,
		[]string{
			"--distribution",
			p.target.Distro,
			"--user",
			p.target.User,
			"--exec",
			"/usr/bin/printenv",
			key,
		},
	)
	if ctx.Err() != nil {
		return "", false, ctx.Err()
	}
	if err != nil {
		return "", false, fmt.Errorf("%w (%s)", ErrProtocol, key)
	}
	value, present, err := parseResult(result)
	if err != nil {
		return "", false, fmt.Errorf("%w (%s)", err, key)
	}
	return value, present, nil
}

func parseResult(result ProbeResult) (string, bool, error) {
	if len(result.Stderr) != 0 || len(result.Stdout) > outputLimit {
		return "", false, ErrProtocol
	}
	if result.ExitCode == 1 && len(result.Stdout) == 0 {
		return "", false, nil
	}
	if result.ExitCode != 0 {
		return "", false, ErrProtocol
	}
	value, err := parseValue(result.Stdout)
	return value, err == nil, err
}

func parseValue(stdout []byte) (string, error) {
	if !utf8.Valid(stdout) || !strings.HasSuffix(string(stdout), "\n") {
		return "", ErrProtocol
	}
	value := strings.TrimSuffix(strings.TrimSuffix(string(stdout), "\n"), "\r")
	if strings.ContainsAny(value, "\x00\r\n") {
		return "", ErrProtocol
	}
	return value, nil
}
