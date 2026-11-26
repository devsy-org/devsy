//go:build linux

package registryfixture

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	fixturePassword    = "fixture-password"
	fixtureServerKey   = "registry.invalid:43210"
	fixtureUser        = "fixture-user"
	fixtureOperationID = "attempt-0001"
)

// This exercises only the owned loopback TLS/proxy/auth fixture. It does not
// execute MicroSandbox or claim that the published provider consumed it.
func TestOwnedLoopbackTLSAndAuthorityLimitedCONNECT(t *testing.T) {
	s, err := Start(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", "sha256:fixture")
		_, _ = io.WriteString(w, "fixture-response")
	}), Config{Username: fixtureUser, Password: fixturePassword})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	defer func() {
		if err := s.Close(ctx); err != nil {
			t.Errorf("close owned fixture: %v", err)
		}
	}()
	client := fixtureHTTPClient(t, s)
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"https://"+s.Authority+"/v2/fixture/manifests/tag",
		nil,
	)
	require.NoError(t, err)
	req.SetBasicAuth(s.Username, s.Password)
	resp, err := client.Do(req)
	require.NoError(t, err)
	body, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	require.NoError(t, readErr)
	require.NoError(t, closeErr)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, string(body), "fixture-response")
	denied, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://outside.invalid/", nil)
	require.NoError(t, err)
	_, err = client.Do(denied)
	require.Error(t, err, "proxy unexpectedly allowed a non-owned CONNECT authority")
	got := s.Snapshot()
	require.Equal(t, 1, got.ConnectAllowed)
	require.Equal(t, 1, got.ConnectDenied)
	require.Equal(t, 1, got.Authenticated)
	require.Zero(t, got.AuthRejected)
	require.Equal(t, 1, got.Completed)
	require.Equal(t, 1, got.ManifestGets)
	require.Positive(t, got.ResponseBytes)
	require.Len(t, got.Requests, 1)
	require.Equal(
		t,
		fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("fixture-response"))),
		got.Requests[0].BodyDigest,
	)
}

func TestRegistryRecordsRejected401AndBodyDigests(t *testing.T) {
	const body = "manifest-body-for-digest"
	s, err := Start(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", "sha256:expected-header")
		_, _ = io.WriteString(w, body)
	}), Config{Username: fixtureUser, Password: fixturePassword})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cleanupRegistryServer(t, s)
	client := fixtureHTTPClient(t, s)
	endpoint := "https://" + s.Authority + "/v2/fixture/manifests/sha256:expected"
	for _, authenticated := range []bool{false, true} {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		require.NoError(t, reqErr)
		if authenticated {
			req.SetBasicAuth(s.Username, s.Password)
		}
		resp, requestErr := client.Do(req)
		require.NoError(t, requestErr, "owned registry request failed")
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		wantStatus := http.StatusUnauthorized
		if authenticated {
			wantStatus = http.StatusOK
		}
		require.Equal(t, wantStatus, resp.StatusCode)
	}
	got := s.Snapshot()
	wantBodyDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body)))
	require.Equal(t, 1, got.AuthRejected)
	require.Equal(t, 1, got.Authenticated)
	require.Equal(t, 2, got.Completed)
	require.Equal(t, 1, got.ManifestGets)
	require.Len(t, got.Requests, 2)
	rejected, success := got.Requests[0], got.Requests[1]
	require.Equal(t, http.StatusUnauthorized, rejected.Status)
	require.False(t, rejected.Authenticated)
	require.Equal(t, "manifests", rejected.Path)
	require.Positive(t, rejected.Bytes)
	require.NotEmpty(t, rejected.BodyDigest)
	require.Equal(t, http.StatusOK, success.Status)
	require.True(t, success.Authenticated)
	require.Equal(t, "manifests", success.Path)
	require.Equal(t, int64(len(body)), success.Bytes)
	require.Equal(t, wantBodyDigest, success.BodyDigest)
	require.Equal(t, "sha256:expected-header", success.Digest)
}

func TestRegistryObservesImplicitOKBeforeLateWriteHeader(t *testing.T) {
	const body = "body commits implicit status"
	s, err := Start(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body)
		w.WriteHeader(http.StatusInternalServerError)
	}), Config{Username: fixtureUser, Password: fixturePassword})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cleanupRegistryServer(t, s)
	client := fixtureHTTPClient(t, s)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://"+s.Authority+"/v2/fixture/manifests/implicit-status", nil)
	require.NoError(t, err)
	req.SetBasicAuth(s.Username, s.Password)
	resp, err := client.Do(req)
	require.NoError(t, err, "owned registry request failed")
	gotBody, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	require.NoError(t, readErr)
	require.NoError(t, closeErr)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, body, string(gotBody))

	got := s.Snapshot()
	wantDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body)))
	require.Equal(t, 1, got.Completed)
	require.Equal(t, 1, got.Authenticated)
	require.Equal(t, int64(len(body)), got.ResponseBytes)
	require.Len(t, got.Requests, 1)
	observed := got.Requests[0]
	require.Equal(t, resp.StatusCode, observed.Status)
	require.Equal(t, int64(len(body)), observed.Bytes)
	require.Equal(t, wantDigest, observed.BodyDigest)
}

func TestCredentialHelperGetSubprocessProtocol(t *testing.T) {
	dir := privateCredentialRoot(t)
	configPath := filepath.Join(dir, "credentials.json")
	logPath := filepath.Join(dir, "helper-observations.jsonl")
	const secret = "private-fixture-secret"
	const operationID = fixtureOperationID
	parentHash, err := executableSHA256()
	require.NoError(t, err, "hash test executable")
	// #nosec G117 -- Synthetic credentials are written only to this private test fixture.
	config, err := json.Marshal(CredentialConfig{
		OperationID:            operationID,
		ServerKey:              fixtureServerKey,
		Username:               fixtureUser,
		Secret:                 secret,
		CredentialsAvailable:   true,
		ExpectedProviderSHA256: parentHash,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, config, 0o600))
	helperPath := linkCredentialHelper(t, dir)
	t.Setenv(CredentialRootEnv, dir)
	t.Setenv("PATH", filepath.Dir(helperPath)+string(os.PathListSeparator)+os.Getenv("PATH"))
	require.True(
		t,
		IsCredentialHelperInvocation(HelperBinaryName),
		"bare helper must resolve to the private helper",
	)
	cmd := credentialHelperCommand(t)
	cmd.Env = append(
		os.Environ(),
		CredentialRootEnv+"="+dir,
		CredentialConfigEnv+"="+configPath,
		CredentialLogEnv+"="+logPath,
		CredentialOperationEnv+"="+operationID,
	)
	cmd.Stdin = strings.NewReader("registry.invalid:43210\n")
	stdout, err := cmd.Output()
	require.NoError(t, err, "credential helper get subprocess failed")
	var got map[string]string
	require.NoError(t, json.Unmarshal(stdout, &got), "credential helper returned invalid JSON")
	require.True(t, got["Username"] == fixtureUser, "credential helper returned an unexpected user")
	require.True(t, got["Secret"] == secret, "credential helper returned an unexpected secret")
	// #nosec G304 -- The path is created beneath the test-owned temporary directory.
	line, err := os.ReadFile(logPath)
	require.NoError(t, err, "credential helper did not record its owned invocation")
	var observation CredentialObservation
	require.NoError(
		t,
		json.Unmarshal([]byte(strings.TrimSpace(string(line))), &observation),
		"credential helper observation is invalid JSON",
	)
	assertSuccessfulHelperObservation(t, observation, parentHash)
	require.False(
		t,
		strings.Contains(string(line), secret),
		"observation leaked the fixture secret",
	)
	assertCredentialNonceRejected(t, dir, secret)

	assertCredentialParentMismatch(t, dir, secret)
}

func assertCredentialNonceRejected(t *testing.T, dir, secret string) {
	t.Helper()
	configPath := filepath.Join(dir, "credentials.json")
	logPath := filepath.Join(dir, "helper-observations.jsonl")
	cmd := credentialHelperCommand(t)
	cmd.Env = append(os.Environ(), CredentialRootEnv+"="+dir, CredentialConfigEnv+"="+configPath,
		CredentialLogEnv+"="+logPath, CredentialOperationEnv+"=different-attempt")
	cmd.Stdin = strings.NewReader("registry.invalid:43210\n")
	output, err := cmd.Output()
	require.Error(t, err, "helper accepted a config from a different attempt")
	require.False(
		t,
		strings.Contains(string(output), secret),
		"helper exposed credentials for a different attempt",
	)
	require.Len(t, readCredentialObservations(t, logPath), 1)
}

func assertSuccessfulHelperObservation(
	t *testing.T,
	observation CredentialObservation,
	parentHash string,
) {
	t.Helper()
	require.Equal(t, fixtureOperationID, observation.OperationID)
	require.Equal(t, fixtureServerKey, observation.ServerKey)
	require.Equal(t, "get", observation.Action)
	require.Equal(t, os.Getpid(), observation.Parent.PID)
	require.Positive(t, observation.Parent.StartTimeTicks)
	require.Equal(t, parentHash, observation.Parent.SHA256)
	require.Equal(t, "credentials_provided", observation.Outcome)
}

func assertCredentialParentMismatch(t *testing.T, dir, secret string) {
	t.Helper()
	const operationID = fixtureOperationID
	configPath := filepath.Join(dir, "credentials.json")
	logPath := filepath.Join(dir, "helper-observations.jsonl")
	// The fixture test process can invoke the helper, but cannot satisfy an
	// unrelated expected provider hash merely by being its recorded parent.
	// #nosec G117 -- Synthetic credentials stay within this private test fixture.
	config, err := json.Marshal(CredentialConfig{
		OperationID:            operationID,
		ServerKey:              fixtureServerKey,
		Username:               fixtureUser,
		Secret:                 secret,
		CredentialsAvailable:   true,
		ExpectedProviderSHA256: strings.Repeat("0", 64),
	})
	require.NoError(t, err, "encode mismatch config")
	require.NoError(t, os.WriteFile(configPath, config, 0o600), "write mismatch config")
	cmd := credentialHelperCommand(t)
	cmd.Env = append(
		os.Environ(),
		CredentialRootEnv+"="+dir,
		CredentialConfigEnv+"="+configPath,
		CredentialLogEnv+"="+logPath,
		CredentialOperationEnv+"="+operationID,
	)
	cmd.Stdin = strings.NewReader("registry.invalid:43210\n")
	stdout, err := cmd.Output()
	require.Error(t, err, "test process satisfied a mismatched pinned-provider hash")
	require.False(
		t,
		strings.Contains(string(stdout), secret),
		"mismatched parent received credentials",
	)
	observations := readCredentialObservations(t, logPath)
	require.Len(t, observations, 2)
	require.Equal(t, "parent_hash_mismatch", observations[1].Outcome)
	require.Equal(t, os.Getpid(), observations[1].Parent.PID)
	require.NotEqual(t, strings.Repeat("0", 64), observations[1].Parent.SHA256)
	// #nosec G304 -- The path is an observation file in the test-owned temporary directory.
	allObservations, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.False(
		t,
		strings.Contains(string(allObservations), secret),
		"observations leaked the fixture secret",
	)
}

func TestCredentialHelperExplicitNotFoundDoesNotReturnCredentials(t *testing.T) {
	dir := privateCredentialRoot(t)
	configPath, logPath := filepath.Join(
		dir,
		"credentials.json",
	), filepath.Join(
		dir,
		"observations.jsonl",
	)
	parentHash, err := executableSHA256()
	require.NoError(t, err, "hash test executable")
	// #nosec G117 -- The deliberate not-found fixture contains no secret.
	config, err := json.Marshal(CredentialConfig{
		OperationID:            "attempt-not-found",
		ServerKey:              fixtureServerKey,
		CredentialsAvailable:   false,
		ExpectedProviderSHA256: parentHash,
	})
	require.NoError(t, err, "encode no-credentials config")
	require.NoError(t, os.WriteFile(configPath, config, 0o600), "write no-credentials config")
	helperPath := linkCredentialHelper(t, dir)
	t.Setenv(CredentialRootEnv, dir)
	t.Setenv("PATH", filepath.Dir(helperPath)+string(os.PathListSeparator)+os.Getenv("PATH"))
	require.True(
		t,
		IsCredentialHelperInvocation(HelperBinaryName),
		"bare helper must resolve to the private helper",
	)
	cmd := credentialHelperCommand(t)
	cmd.Env = append(
		os.Environ(),
		CredentialRootEnv+"="+dir,
		CredentialConfigEnv+"="+configPath,
		CredentialLogEnv+"="+logPath,
		CredentialOperationEnv+"=attempt-not-found",
	)
	cmd.Stdin = strings.NewReader("registry.invalid:43210\n")
	stdout, err := cmd.Output()
	require.Error(t, err)
	require.True(
		t,
		strings.TrimSpace(string(stdout)) == credentialsNotFoundMessage,
		"unexpected not-found response",
	)
	observations := readCredentialObservations(t, logPath)
	require.Len(t, observations, 1)
	require.Equal(t, "attempt-not-found", observations[0].OperationID)
	require.Equal(t, "credentials_not_found", observations[0].Outcome)
	require.Equal(t, os.Getpid(), observations[0].Parent.PID)
	require.False(
		t,
		strings.Contains(string(stdout), "Secret"),
		"not-found response contains credential fields",
	)
}

func TestCredentialFilesRequirePrivateOwnedRootAndRegularFiles(t *testing.T) {
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	require.NoError(t, err, "resolve private root")
	// #nosec G302 -- This is a private directory, so owner execute permission is required.
	require.NoError(t, os.Chmod(root, 0o700), "chmod private root")
	configPath, logPath := filepath.Join(
		root,
		"config.json",
	), filepath.Join(
		root,
		"observations.jsonl",
	)
	// #nosec G117 -- This configuration contains no credentials and stays in the test-owned root.
	validConfig, err := json.Marshal(
		CredentialConfig{OperationID: "attempt", ServerKey: "registry.invalid:1234"},
	)
	require.NoError(t, err, "encode config")
	writeConfig := func(path string, mode os.FileMode) {
		t.Helper()
		require.NoError(t, os.WriteFile(path, validConfig, mode), "write config")
		require.NoError(t, os.Chmod(path, mode), "set config mode")
	}
	writeConfig(configPath, 0o600)
	cfg, err := ReadCredentialConfig(root, configPath)
	require.NoError(t, err, "valid owned config rejected")
	require.Equal(t, "attempt", cfg.OperationID)

	loose := filepath.Join(root, "loose.json")
	writeConfig(loose, 0o644)
	_, err = ReadCredentialConfig(root, loose)
	require.Error(t, err, "accepted group/world-readable config")
	link := filepath.Join(root, "linked.json")
	require.NoError(t, os.Symlink(configPath, link), "create config symlink")
	_, err = ReadCredentialConfig(root, link)
	require.Error(t, err, "accepted symlink config")
	outside := filepath.Join(t.TempDir(), "outside.json")
	writeConfig(outside, 0o600)
	_, err = ReadCredentialConfig(root, outside)
	require.Error(t, err, "accepted config outside private root")
	require.NoError(t, os.Symlink(outside, logPath), "create log symlink")
	require.Error(
		t,
		appendOwnedLine(root, logPath, []byte("{}\n")),
		"accepted symlink observation log",
	)
	require.Error(
		t,
		appendOwnedLine(
			root,
			filepath.Join(filepath.Dir(root), "outside-log.jsonl"),
			[]byte("{}\n"),
		),
		"accepted observation log outside private root",
	)
}

func TestCredentialHelperDispatchRequiresOwnedPathAndSameExecutable(t *testing.T) {
	root := privateCredentialRoot(t)
	binDir := filepath.Join(root, "bin")
	require.NoError(t, os.Mkdir(binDir, 0o700), "create helper bin directory")
	helpPath := filepath.Join(binDir, HelperBinaryName)
	require.NoError(t, os.Symlink("/bin/true", helpPath), "create foreign helper link")
	t.Setenv(CredentialRootEnv, root)
	require.False(t, IsCredentialHelperInvocation(helpPath), "dispatch accepted another executable")
	require.False(
		t,
		IsCredentialHelperInvocation(os.Args[0]),
		"dispatch accepted the ordinary test binary",
	)
}

func TestCredentialHelperDispatchResolvesOnlyPrivateBareHelper(t *testing.T) {
	root := privateCredentialRoot(t)
	helperPath := linkCredentialHelper(t, root)
	t.Setenv(CredentialRootEnv, root)
	privateBin := filepath.Dir(helperPath)
	t.Setenv("PATH", privateBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	require.True(
		t,
		IsCredentialHelperInvocation(HelperBinaryName),
		"dispatch rejected the private bare name",
	)
	require.True(
		t,
		IsCredentialHelperInvocation(helperPath),
		"dispatch rejected the private absolute path",
	)
	require.False(
		t,
		IsCredentialHelperInvocation(filepath.Join("bin", HelperBinaryName)),
		"accepted another relative path",
	)

	foreignBin := t.TempDir()
	executable, err := os.Executable()
	require.NoError(t, err, "locate current executable")
	require.NoError(
		t,
		os.Symlink(executable, filepath.Join(foreignBin, HelperBinaryName)),
		"create foreign PATH helper",
	)
	t.Setenv(
		"PATH",
		foreignBin+string(
			os.PathListSeparator,
		)+privateBin+string(
			os.PathListSeparator,
		)+os.Getenv(
			"PATH",
		),
	)
	require.False(
		t,
		IsCredentialHelperInvocation(HelperBinaryName),
		"accepted a helper from a foreign PATH directory",
	)
}

func privateCredentialRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err, "resolve helper root")
	// #nosec G302 -- This private directory requires owner execute permission.
	require.NoError(t, os.Chmod(dir, 0o700), "set helper root mode")
	return dir
}

func linkCredentialHelper(t *testing.T, dir string) string {
	t.Helper()
	binDir := filepath.Join(dir, "bin")
	require.NoError(t, os.Mkdir(binDir, 0o700), "create private helper bin directory")
	executable, err := os.Executable()
	require.NoError(t, err, "locate fixture test executable")
	helperPath := filepath.Join(binDir, HelperBinaryName)
	require.NoError(
		t,
		os.Symlink(executable, helperPath),
		"link fixture test executable as credential helper",
	)
	return helperPath
}

func executableSHA256() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	// #nosec G304 -- This is the current test executable returned by os.Executable.
	file, err := os.Open(executable)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, file)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func readCredentialObservations(t *testing.T, path string) []CredentialObservation {
	t.Helper()
	// #nosec G304 -- Callers pass the observation file in their private temporary fixture.
	data, err := os.ReadFile(path)
	require.NoError(t, err, "read helper observations")
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	observations := make([]CredentialObservation, 0, len(lines))
	for _, line := range lines {
		var observation CredentialObservation
		require.NoError(t, json.Unmarshal([]byte(line), &observation), "decode helper observation")
		observations = append(observations, observation)
	}
	return observations
}

func credentialHelperCommand(t *testing.T) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return exec.CommandContext(ctx, HelperBinaryName, "get")
}

func fixtureHTTPClient(t *testing.T, server *Server) *http.Client {
	t.Helper()
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(server.CAPEM), "owned CA PEM was not parseable")
	proxyURL, err := url.Parse(server.ProxyURL)
	require.NoError(t, err)
	transport := &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}
}

func cleanupRegistryServer(t *testing.T, server *Server) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 3*time.Second)
		defer cancel()
		require.NoError(t, server.Close(ctx), "close owned registry fixture")
	})
}

func TestCloseRetainsFirstResult(t *testing.T) {
	s, err := Start(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		Config{Username: "u", Password: "p"},
	)
	require.NoError(t, err)
	// Hold an owned activity slot so an already-canceled first close must
	// report its bounded-wait error deterministically.
	s.tunnelMu.Lock()
	s.trackStartLocked()
	s.tunnelMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	first := s.Close(ctx)
	s.trackDone()
	second := s.Close(context.Background())
	require.Error(t, first)
	require.Error(t, second)
	require.EqualError(t, second, first.Error(), "Close did not retain its first cleanup result")
}

func TestCloseRacingOwnedCONNECTsLeavesNoTrackedWork(t *testing.T) {
	s, err := Start(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "owned")
	}), Config{Username: "u", Password: "p"})
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cleanupCancel()
		_ = s.Close(cleanupCtx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	client := fixtureHTTPClient(t, s)
	start := make(chan struct{})
	done := make(chan struct{}, 24)
	for range 24 {
		go func() {
			defer func() { done <- struct{}{} }()
			<-start
			req, reqErr := http.NewRequestWithContext(
				ctx,
				http.MethodGet,
				"https://"+s.Authority+"/v2/fixture/manifests/tag",
				nil,
			)
			if reqErr != nil {
				return
			}
			req.SetBasicAuth(s.Username, s.Password)
			resp, reqErr := client.Do(req)
			if reqErr == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}()
	}
	close(start)
	require.NoError(t, s.Close(ctx), "close owned fixture")
	for range 24 {
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("owned client requests did not finish before the bound")
		}
	}
	s.tunnelMu.Lock()
	active, tunnels := s.active, len(s.tunnels)
	s.tunnelMu.Unlock()
	require.Zero(t, active, "fixture retained work after close")
	require.Zero(t, tunnels, "fixture retained tunnels after close")
}
