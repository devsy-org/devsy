package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	// maxCaptureBytes bounds each captured stream; older output is dropped.
	maxCaptureBytes = 64 << 10
	// waitDelay bounds how long Wait lingers on pipes held by orphaned children.
	waitDelay = 5 * time.Second
	redacted  = "[REDACTED]"
	// minSecretLen avoids redacting trivially short values everywhere.
	minSecretLen = 6
)

// ExecRunner runs real subprocesses with bounded capture and a hard timeout.
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
	stdout, stderr := newTailBuffer(maxCaptureBytes), newTailBuffer(maxCaptureBytes)
	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = overlayEnv(os.Environ(), c.Env, runtime.GOOS == "windows")
	cmd.WaitDelay = waitDelay
	cmd.Stdout = teeWriter(stdout, c.Stdout)
	cmd.Stderr = teeWriter(stderr, c.Stderr)

	err := cmd.Run()
	res := CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
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
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	res.ExitCode = -1
	if ctxErr := ctx.Err(); ctxErr != nil {
		return res, ctxErr
	}
	// Report the path only: argv may carry sensitive values.
	return res, fmt.Errorf("run %s: %w", c.Path, err)
}

func teeWriter(capture io.Writer, live io.Writer) io.Writer {
	if live == nil {
		return capture
	}
	return io.MultiWriter(capture, live)
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	mu      sync.Mutex
	limit   int
	buf     []byte
	dropped int64
}

func newTailBuffer(limit int) *tailBuffer { return &tailBuffer{limit: limit} }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(p)
	switch {
	case n >= t.limit:
		t.dropped += int64(len(t.buf) + n - t.limit)
		t.buf = append(t.buf[:0], p[n-t.limit:]...)
	case len(t.buf)+n > t.limit:
		drop := len(t.buf) + n - t.limit
		t.dropped += int64(drop)
		t.buf = append(t.buf[:0], t.buf[drop:]...)
		t.buf = append(t.buf, p...)
	default:
		t.buf = append(t.buf, p...)
	}
	return n, nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := strings.ToValidUTF8(string(t.buf), "")
	if t.dropped > 0 {
		return fmt.Sprintf("[%d earlier bytes dropped]\n%s", t.dropped, s)
	}
	return s
}

// overlayEnv returns base with every overlay entry applied: an overlay NAME
// replaces all same-named base entries (case-insensitive when foldCase, as on
// Windows) instead of appending a duplicate. Later overlay entries win.
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
func lookupEnv(env []string, name string, foldCase bool) (string, bool) {
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
func redact(s string, secrets ...string) string {
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
