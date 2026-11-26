package ghcredentials

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/credentials"
	"github.com/devsy-org/devsy/pkg/log"
)

const (
	sessionEnv   = "DEVSY_GH_SESSION"
	authProbeEnv = "DEVSY_GH_AUTH_PROBE"
)

func Run(args []string) int {
	if len(args) == 0 {
		log.Error("gh relay: wrapper path missing")
		return 127
	}
	binary, err := realBinary(args[0])
	if err != nil {
		log.Errorw("gh relay: executable unavailable", "error_type", errorKind(err))
		return 127
	}
	log.Debugw("gh relay: executable selected", "binary", binary)
	env := os.Environ()
	if _, explicit := os.LookupEnv("GH_PATH"); !explicit {
		env = replaceEnv(env, "GH_PATH", wrapperPath)
	}
	// A local lookup may re-enter through another dispatcher. Keep forwarding
	// disabled throughout that process tree, even if it changes GH_CONFIG_DIR.
	if localOnlySession() {
		return runGH(binary, args[1:], env, nil)
	}
	inv := &invocation{binary: binary, args: args[1:], env: env}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	inv.recover(ctx)
	if forwardingDisabled() {
		return runGH(binary, inv.args, env, nil)
	}
	session := inv.prepare(ctx)
	if session == nil {
		return runGH(binary, inv.args, env, nil)
	}
	return inv.withSession(session)
}

type invocation struct {
	binary    string
	args, env []string
}

func (inv *invocation) recover(ctx context.Context) {
	root, err := sessionRoot()
	if err != nil {
		logFailure("gh relay: recovery unavailable", "session.recover", err, false)
		return
	}
	l, err := lock(ctx, filepath.Join(root, "sessions.lock"))
	if err != nil {
		logFailure("gh relay: recovery unavailable", "session.recover_lock", err, false)
		return
	}
	defer func() { _ = l.Close() }()
	recoverSessions(ctx, root)
}

func (inv *invocation) prepare(ctx context.Context) *session {
	result, err := fetch(ctx)
	if err != nil {
		logFailure("gh relay: forwarding unavailable", "relay.fetch", err, false)
		return nil
	}
	log.Debugw("gh relay: host authentication", "status", result.Status)
	switch result.Status {
	case stateAvailable, "gh_not_found", hostNoActiveAccounts, "rpc_denied":
	default:
		log.Warnw(
			"gh relay: forwarding unavailable",
			"operation",
			"host.resolve",
			"status",
			result.Status,
		)
	}
	if len(result.Accounts) == 0 {
		return nil
	}
	original, err := ConfigDir()
	if err != nil {
		logFailure("gh relay: forwarding unavailable", "config.path", err, false)
		return nil
	}
	s, err := startSession(ctx, original, result.Accounts)
	if err != nil {
		logFailure("gh relay: forwarding unavailable", "session.snapshot", err, false)
		return nil
	}
	if err := s.inject(ctx, inv.binary, inv.env); err != nil {
		logFailure("gh relay: forwarding unavailable", "config.overlay", err, false)
		_ = os.RemoveAll(s.dir)
		_ = s.lock.Close()
		return nil
	}
	return s
}

func (inv *invocation) withSession(s *session) int {
	env := replaceEnv(inv.env, "GH_CONFIG_DIR", s.work())
	env = replaceEnv(env, sessionEnv, s.work())
	code := runGH(inv.binary, inv.args, env, s.lock.file)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.finish(ctx); err != nil {
		logFailure("gh relay: configuration writeback pending", "config.writeback", err, true)
	}
	return code
}

func realBinary(wrapper string) (string, error) {
	self, _ := os.Stat(wrapper)
	backup := wrapperBackup(wrapper, self)
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		path := filepath.Join(dir, "gh")
		info, err := executableInfo(path)
		if err != nil {
			continue
		}
		if self != nil && os.SameFile(self, info) {
			if backup != "" {
				return filepath.Abs(backup)
			}
			continue
		}
		return filepath.Abs(path)
	}
	if backup != "" {
		return filepath.Abs(backup)
	}
	return "", exec.ErrNotFound
}

func wrapperBackup(wrapper string, self os.FileInfo) string {
	installed, err := filepath.EvalSymlinks(wrapper)
	if err != nil {
		if !os.IsNotExist(err) {
			return ""
		}
		installed = wrapper
	}
	backup := installed + backupSuffix
	original, err := executableInfo(backup)
	if err != nil || (self != nil && os.SameFile(self, original)) {
		return ""
	}
	if own, err := isWrapper(backup); err != nil || own {
		return ""
	}
	return backup
}

func replaceEnv(env []string, name, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, name+"=") {
			out = append(out, e)
		}
	}
	return append(out, name+"="+value)
}

func fetch(ctx context.Context) (HostResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	number, err := relayPort()
	if err != nil {
		return HostResult{}, err
	}
	ready, err := credentials.ProbeReadiness(
		ctx,
		number,
		credentials.Readiness{
			HostOwner:     os.Getenv("DEVSY_GH_HOST_OWNER"),
			Instance:      os.Getenv("DEVSY_GH_INSTANCE"),
			GitHubAllowed: true,
		},
	)
	if err != nil {
		return HostResult{}, boundary("relay.readiness", err)
	}
	client := credentials.LoopbackHTTPClient()
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"http://"+ready.Address+"/github-credentials",
		nil,
	)
	if err != nil {
		return HostResult{}, boundary("relay.http", err)
	}
	req.Header.Set(credentials.HeaderCredentialsInstance, ready.Instance)
	req.Header.Set(credentials.HeaderCredentialsHostOwner, ready.HostOwner)
	resp, err := client.Do(req)
	if err != nil {
		return HostResult{}, boundary("relay.http", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return HostResult{}, &boundaryError{
			operation: "relay.http",
			kind:      "http_" + strconv.Itoa(resp.StatusCode),
		}
	}
	return decodeHostResult(resp.Body)
}

func decodeHostResult(body io.Reader) (HostResult, error) {
	b, err := io.ReadAll(io.LimitReader(body, (1<<20)+1))
	if err != nil {
		return HostResult{}, boundary("relay.read", err)
	}
	var result HostResult
	if len(b) > 1<<20 {
		return HostResult{}, &boundaryError{operation: "relay.decode", kind: "response_limit"}
	}
	err = json.Unmarshal(b, &result)
	if err != nil {
		return HostResult{}, &boundaryError{
			operation: "relay.decode",
			kind:      "schema_invalid",
			cause:     err,
		}
	}
	return result, nil
}

func executableInfo(path string) (os.FileInfo, error) {
	f, err := resolvedFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return nil, exec.ErrNotFound
	}
	return info, nil
}

func relayPort() (int, error) {
	port := os.Getenv("DEVSY_CREDENTIALS_SERVER_PORT")
	if port == "" {
		port = "12049"
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return 0, &boundaryError{operation: "relay.port", kind: "config_invalid"}
	}
	return number, nil
}

func forwardingDisabled() bool {
	value := os.Getenv("DEVSY_GH_FORWARDING")
	return value == "disabled" || value == stateUnavailable
}

func localOnlySession() bool {
	active := os.Getenv(sessionEnv)
	return os.Getenv(authProbeEnv) == "1" || (active != "" && active == os.Getenv("GH_CONFIG_DIR"))
}
