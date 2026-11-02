package ghcredentials

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"gopkg.in/yaml.v3"
)

const hostNoActiveAccounts = "no_active_accounts"

type Account struct {
	Host  string `json:"host"`
	User  string `json:"user"`
	Token string `json:"token"`
}

func HostAccounts(ctx context.Context) HostResult {
	ctx, cancel := context.WithTimeout(ctx, hostCollectionTimeout)
	defer cancel()
	binary, err := exec.LookPath("gh")
	if err != nil {
		return HostResult{Status: "gh_not_found"}
	}
	dir, err := ConfigDir()
	if err != nil {
		return HostResult{Status: "config_path_failed"}
	}
	probe, err := os.MkdirTemp("", "devsy-gh-host-")
	if err != nil {
		return HostResult{Status: "config_snapshot_" + errorKind(err)}
	}
	defer func() { _ = os.RemoveAll(probe) }()
	if err := copyConfigFiles(dir, probe); err != nil {
		return HostResult{Status: "config_snapshot_" + errorKind(err)}
	}
	b, err := readFile(filepath.Join(probe, hostsFile))
	if err != nil {
		return HostResult{Status: "config_read_" + errorKind(err)}
	}
	hosts, err := decode(b)
	if err != nil {
		return HostResult{Status: "config_decode_failed"}
	}
	return collectHostAccounts(
		ctx,
		binary,
		hosts,
		replaceEnv(hostLookupEnv(os.Environ()), "GH_CONFIG_DIR", probe),
	)
}

func collectHostAccounts(
	ctx context.Context,
	binary string,
	hosts *yaml.Node,
	env []string,
) HostResult {
	ctx, cancel := context.WithTimeout(ctx, hostCollectionTimeout)
	defer cancel()
	result := HostResult{Status: hostNoActiveAccounts}
	accounts, truncated := registeredHostAccounts(hosts)
	if truncated {
		result.Status = "account_limit"
	}
	results := resolveHostAccounts(ctx, binary, accounts, env)
	for _, r := range results {
		if r.err != nil {
			result.Status = "token_lookup_failed"
		} else if r.account.valid() {
			result.Accounts = append(result.Accounts, r.account)
		}
	}
	if len(result.Accounts) > 0 && result.Status == hostNoActiveAccounts {
		result.Status = stateAvailable
	}
	return result
}

func registeredHostAccounts(hosts *yaml.Node) ([]Account, bool) {
	var accounts []Account
	for i := 0; i+1 < len(hosts.Content); i += 2 {
		host := hosts.Content[i].Value
		u := get(hosts.Content[i+1], "user")
		if u == nil || u.Value == "" {
			log.Debugw("gh relay: host account unavailable", "host", host)
			continue
		}
		if len(accounts) == maxHostAccounts {
			return accounts, true
		}
		accounts = append(accounts, Account{Host: host, User: u.Value})
	}
	return accounts, false
}

type accountLookup struct {
	account Account
	err     error
}

func resolveHostAccounts(
	ctx context.Context,
	binary string,
	accounts []Account,
	env []string,
) []accountLookup {
	results := make([]accountLookup, len(accounts))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(4, len(accounts)) {
		workers.Go(func() {
			for i := range jobs {
				results[i].account, results[i].err = lookupHostAccount(
					ctx,
					binary,
					accounts[i],
					env,
				)
			}
		})
	}
	for i := range accounts {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	return results
}

const (
	hostCollectionTimeout = 3500 * time.Millisecond
	tokenLookupTimeout    = 1500 * time.Millisecond
	maxHostAccounts       = 32
	maxTokenOutput        = 64 << 10
)

func lookupHostAccount(
	ctx context.Context,
	binary string,
	a Account,
	env []string,
) (Account, error) {
	if err := ctx.Err(); err != nil {
		return Account{}, err
	}
	dir, err := os.MkdirTemp("", "devsy-gh-account-")
	if err != nil {
		return Account{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := copyConfigFiles(envValue(env, "GH_CONFIG_DIR"), dir); err != nil {
		return Account{}, err
	}
	return a.resolveHostToken(ctx, binary, replaceEnv(hostLookupEnv(env), "GH_CONFIG_DIR", dir))
}

func hostLookupEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if !authEnvironment[strings.ToUpper(name)] {
			out = append(out, entry)
		}
	}
	return out
}

func envValue(env []string, key string) string {
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, key) {
			return value
		}
	}
	return ""
}

func (a Account) resolveHostToken(
	ctx context.Context,
	binary string,
	env []string,
) (Account, error) {
	token, err := resolveToken(ctx, binary, a.Host, env)
	if err != nil {
		if noTokenResult(err) {
			log.Debugw("gh relay: host account unavailable", "host", a.Host)
			return Account{}, nil
		}
		log.Warnw(
			"gh relay: host token lookup failed",
			"host",
			a.Host,
			"error_type",
			errorKind(err),
		)
		return Account{}, err
	}
	a.Token = token
	return a, nil
}

func resolveToken(ctx context.Context, binary, host string, env []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, tokenLookupTimeout)
	defer cancel()
	//nolint:gosec // binary is resolved from the user's PATH; host is one argument.
	cmd := exec.CommandContext(ctx, binary, "auth", "token", "--hostname", host)
	// Scope this to the lookup child: nested wrappers must not return forwarded
	// credentials as evidence of local authentication.
	cmd.Env = replaceEnv(env, authProbeEnv, "1")
	cmd.WaitDelay = 250 * time.Millisecond
	out := &tokenOutput{}
	err := runTokenProbe(cmd, out)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if out.overflow {
		return "", errTokenOutputLimit
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out.data)), nil
}

func runTokenProbe(cmd *exec.Cmd, out io.Writer) error {
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	defer func() { _ = stdout.Close(); _ = stdoutWriter.Close() }()
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	defer func() { _ = stderr.Close(); _ = stderrWriter.Close() }()
	cmd.Stdout, cmd.Stderr = stdoutWriter, stderrWriter
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	done := make(chan error, 2)
	go func() { _, err := io.Copy(out, stdout); done <- err }()
	go func() { _, err := io.Copy(io.Discard, stderr); done <- err }()
	processErr := cmd.Wait()
	timer := time.NewTimer(cmd.WaitDelay)
	defer timer.Stop()
	var readErr error
	for received := 0; received < 2; received++ {
		select {
		case err := <-done:
			readErr = errors.Join(readErr, err)
		case <-timer.C:
			_ = stdout.Close()
			_ = stderr.Close()
			for ; received < 2; received++ {
				<-done
			}
			return exec.ErrWaitDelay
		}
	}
	return errors.Join(processErr, readErr)
}

var errTokenOutputLimit = errors.New("GitHub CLI token output limit exceeded")

type tokenOutput struct {
	data     []byte
	overflow bool
}

func (w *tokenOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := maxTokenOutput - len(w.data)
	if n > remaining {
		w.overflow = true
		p = p[:remaining]
	}
	w.data = append(w.data, p...)
	return n, nil
}
