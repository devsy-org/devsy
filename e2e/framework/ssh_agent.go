package framework

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
)

// testLogger is the minimal subset of testing.TB / Ginkgo's FullGinkgoTInterface
// used by these helpers. Keeping it narrow lets callers pass either
// ginkgo.GinkgoT() or a real *testing.T without an awkward type assertion.
type testLogger interface {
	Helper()
	Fatalf(format string, args ...any)
}

// StartMockSSHAgent starts an isolated ssh-agent on a temporary socket path
// and loads a freshly generated ed25519 key into it. The returned cleanup
// function kills the agent process and removes the temp directory.
//
// Accepts a narrow testLogger interface satisfied by both *testing.T and
// ginkgo.GinkgoT().
func StartMockSSHAgent(t testLogger) (authSock string, pubKey string, cleanup func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "devsy-mock-ssh-agent-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}

	// Use a short socket path; long /tmp paths can exceed UNIX_PATH_MAX.
	sockPath := filepath.Join(tmpDir, "agent.sock")
	keyPath := filepath.Join(tmpDir, "id_ed25519")

	if err := generateAgentKey(keyPath); err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("%v", err)
	}

	agentCmd, agentStderr, err := startSSHAgentProcess(sockPath)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("%v", err)
	}

	killAgent := func() {
		if agentCmd.Process != nil {
			_ = agentCmd.Process.Kill()
		}
		_ = agentCmd.Wait()
	}

	if err := waitForAgentSocket(sockPath); err != nil {
		killAgent()
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("%v; stderr: %s", err, agentStderr.String())
	}

	if err := addKeyToAgent(sockPath, keyPath); err != nil {
		killAgent()
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("%v", err)
	}

	pubBytes, err := os.ReadFile(keyPath + ".pub") // #nosec G304
	if err != nil {
		killAgent()
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("read pubkey: %v", err)
	}
	pubKey = strings.TrimSpace(string(pubBytes))

	cleanup = func() {
		killAgent()
		_ = os.RemoveAll(tmpDir)
	}
	return sockPath, pubKey, cleanup
}

func generateAgentKey(keyPath string) error {
	// #nosec G204 -- test helper with controlled inputs
	out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-f", keyPath, "-N", "", "-q").
		CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh-keygen: %w: %s", err, out)
	}
	return nil
}

// startSSHAgentProcess runs ssh-agent in the foreground (-D) bound to an
// explicit socket path (-a). Starting it via cmd.Start() avoids the
// daemonize/parent-exit race where the parent could return before the child
// bound the socket.
func startSSHAgentProcess(sockPath string) (*exec.Cmd, *bytes.Buffer, error) {
	// #nosec G204 -- test helper with controlled inputs
	cmd := exec.Command("ssh-agent", "-D", "-a", sockPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("ssh-agent start: %w", err)
	}
	return cmd, &stderr, nil
}

// waitForAgentSocket polls until the agent's socket file appears so subsequent
// ssh-add invocations have something to connect to.
func waitForAgentSocket(sockPath string) error {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(sockPath); statErr == nil {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("ssh-agent did not bind socket %s within 2s", sockPath)
}

func addKeyToAgent(sockPath, keyPath string) error {
	cmd := exec.Command("ssh-add", keyPath) // #nosec G204
	cmd.Env = append(os.Environ(), "SSH_AUTH_SOCK="+sockPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh-add: %w: %s", err, out)
	}
	return nil
}

// OpenSSHControlMaster starts an OpenSSH ControlMaster connection to the given
// devsy workspace host (typically "<workspace>.devsy", as written by
// `devsy up`'s SSH config integration). Multiple sessions can then be opened
// over the same connection by re-invoking ssh with `-S <controlPath>`.
//
// Returns the controlPath, the host the caller should pass to multiplexed
// ssh invocations, and a closer that tears the master down.
//
// `extraEnv` is merged into the child ssh process environment (e.g. to set
// SSH_AUTH_SOCK for agent forwarding).
//
// Accepts a narrow testLogger interface satisfied by both *testing.T and
// ginkgo.GinkgoT().
func OpenSSHControlMaster(
	t testLogger,
	workspaceHost string,
	extraEnv map[string]string,
) (controlPath string, closer func(), err error) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "devsy-ssh-cm-")
	if err != nil {
		return "", nil, fmt.Errorf("mkdtemp: %w", err)
	}
	controlPath = filepath.Join(tmpDir, "cm.sock")

	// Spin the master in the background. -N: no command, -f: background after
	// auth, -M: master mode, ControlPersist gives a window before timeout.
	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ControlMaster=yes",
		"-o", "ControlPath=" + controlPath,
		"-o", "ControlPersist=120",
		"-o", "ForwardAgent=yes",
		"-N", "-f",
		workspaceHost,
	}
	// #nosec G204 -- controlled args for test
	cmd := exec.Command("ssh", args...)
	cmd.Env = mergedEnv(extraEnv)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", nil, fmt.Errorf("ssh ControlMaster start: %w: %s", err, stderr.String())
	}

	closer = func() {
		// Best-effort: ask the master to exit.
		exitCmd := exec.Command(
			"ssh",
			"-O",
			"exit",
			"-o",
			"ControlPath="+controlPath,
			workspaceHost,
		) // #nosec G204
		exitCmd.Env = mergedEnv(extraEnv)
		_ = exitCmd.Run()
		_ = os.RemoveAll(tmpDir)
	}
	return controlPath, closer, nil
}

// SSHMultiplexedExec runs a command over an existing OpenSSH ControlMaster
// connection identified by controlPath. Each invocation is a new SSH "session"
// on the same underlying connection — exactly the scenario that asserts
// $SSH_AUTH_SOCK is stable across sessions (regression for the per-session
// socket allocation bug).
func SSHMultiplexedExec(
	controlPath, host string,
	extraEnv map[string]string,
	cmd ...string,
) (stdout, stderr string, err error) {
	if len(cmd) == 0 {
		return "", "", errors.New("SSHMultiplexedExec: empty command")
	}
	args := []string{
		"-o", "ControlPath=" + controlPath,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		host,
		"--",
	}
	// ssh joins remote argv with spaces before handing it to the remote
	// login shell, which loses any in-argument quoting. Pre-quote each
	// element so shell metacharacters (spaces, $, quotes) survive intact.
	for _, a := range cmd {
		args = append(args, shellescape.Quote(a))
	}

	// #nosec G204 -- controlled args for test
	c := exec.Command("ssh", args...)
	c.Env = mergedEnv(extraEnv)
	var outBuf, errBuf bytes.Buffer
	c.Stdout = &outBuf
	c.Stderr = &errBuf
	err = c.Run()
	return outBuf.String(), errBuf.String(), err
}

// mergedEnv builds an env slice from os.Environ() with the given overrides
// applied last (so they win on duplicate keys).
func mergedEnv(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}
