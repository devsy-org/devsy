package runenv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/livetee"
)

const (
	// waitDelay bounds how long Wait lingers on pipes held by orphaned children.
	waitDelay = 5 * time.Second
	redacted  = "[REDACTED]"
	// minSecretLen avoids redacting trivially short values everywhere.
	minSecretLen = 6
)

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, c Command) (CommandResult, error) {
	if c.Path == "" {
		return CommandResult{ExitCode: -1}, errors.New("empty command path")
	}
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	var stdout, stderr bytes.Buffer
	outTee, errTee := livetee.New(&stdout, c.Stdout), livetee.New(&stderr, c.Stderr)
	//nolint:gosec // G204: ExecRunner runs the command its caller specified, by design
	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	if c.CancelProcessGroup {
		docker.PrepareForGroupCancellation(cmd)
	}
	cmd.Dir = c.Dir
	cmd.Env = overlayEnv(os.Environ(), c.Env, runtime.GOOS == "windows")
	cmd.WaitDelay = waitDelay
	cmd.Stdout = outTee
	cmd.Stderr = errTee

	err := cmd.Run()
	outDropped, errDropped := outTee.Close(), errTee.Close()
	res := CommandResult{
		Stdout:        stdout.String(),
		Stderr:        stderr.String(),
		StdoutDropped: outDropped,
		StderrDropped: errDropped,
	}
	return finishRun(ctx, c.Path, res, err)
}

func finishRun(
	ctx context.Context,
	path string,
	res CommandResult,
	err error,
) (CommandResult, error) {
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	if err == nil {
		return res, nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		res.ExitCode, res.TimedOut = -1, true
		return res, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		res.ExitCode = -1
		return res, ctxErr
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	res.ExitCode = -1
	if ctxErr := ctx.Err(); ctxErr != nil {
		return res, ctxErr
	}
	// Report the path only: argv may carry sensitive values.
	return res, fmt.Errorf("run %s: %w", path, err)
}

func overlayEnv(base, overlay []string, foldCase bool) []string {
	if len(overlay) == 0 {
		return base
	}
	norm := envNormalizer(foldCase)
	// last maps each overlay name to its final entry; earlier duplicates lose.
	last := make(map[string]int, len(overlay))
	for i, kv := range overlay {
		last[norm(envKey(kv))] = i
	}
	out := make([]string, 0, len(base)+len(overlay))
	for _, kv := range base {
		if _, replaced := last[norm(envKey(kv))]; !replaced {
			out = append(out, kv)
		}
	}
	for i, kv := range overlay {
		if last[norm(envKey(kv))] == i {
			out = append(out, kv)
		}
	}
	return out
}

func envNormalizer(foldCase bool) func(string) string {
	if foldCase {
		return strings.ToUpper
	}
	return func(k string) string { return k }
}

// envKey returns the name of a NAME=value entry. Windows keeps hidden entries
// such as "=C:=C:\\dir" whose name starts with '='.
func envKey(kv string) string {
	start := 0
	if strings.HasPrefix(kv, "=") {
		start = 1
	}
	if i := strings.IndexByte(kv[start:], '='); i >= 0 {
		return kv[:start+i]
	}
	return kv
}

// lookupEnv finds name in a NAME=value list; the last match wins.
func LookupEnv(env []string, name string, foldCase bool) (string, bool) {
	var (
		val   string
		found bool
	)
	for _, kv := range env {
		k := envKey(kv)
		if k == name || (foldCase && strings.EqualFold(k, name)) {
			val, found = strings.TrimPrefix(kv[len(k):], "="), true
		}
	}
	return val, found
}

var redactions = []struct {
	re   *regexp.Regexp
	with string
}{
	{
		regexp.MustCompile(
			`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`,
		),
		"-----BEGIN PRIVATE KEY----- " + redacted + " -----END PRIVATE KEY-----",
	},
	{
		regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`),
		redacted,
	},
	{
		regexp.MustCompile(`(?i)(authorization:\s*(?:bearer|basic|token)\s+)\S+`),
		"${1}" + redacted,
	},
	{
		regexp.MustCompile(`(?i)("(?:auth|identitytoken|registrytoken|password)"\s*:\s*)"[^"]*"`),
		`${1}"` + redacted + `"`,
	},
	{
		regexp.MustCompile(`(?i)(https?://[^/\s:@]+:)[^@\s/]+@`),
		"${1}" + redacted + "@",
	},
	{
		regexp.MustCompile(
			`(?i)\b([A-Za-z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|PRIVATE_KEY|ACCESS_KEY)` +
				`[A-Za-z0-9_]*)(\s*[=:]\s*)("[^"]*"|'[^']*'|\S+)`,
		),
		"${1}${2}" + redacted,
	},
}

// redact scrubs known secret shapes plus any exact secret values from s. It is
// a bounded best-effort sanitizer for child output, never a security boundary.
func Redact(s string, secrets ...string) string {
	for _, secret := range secrets {
		if len(secret) >= minSecretLen {
			s = strings.ReplaceAll(s, secret, redacted)
		}
	}
	for _, r := range redactions {
		s = r.re.ReplaceAllString(s, r.with)
	}
	return s
}
