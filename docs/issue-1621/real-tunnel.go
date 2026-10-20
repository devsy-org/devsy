//go:build ignore

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/devsy-org/devsy/pkg/inject"
	"golang.org/x/crypto/ssh"
)

type tunnelConfig struct {
	provider, host, port, builtin, dest, payload, check string
}

type tunnelRun struct {
	config    tunnelConfig
	bridge    net.Conn
	stderr    bytes.Buffer
	requests  int
	completed chan map[string]any
}

func main() {
	result := runTunnel(parseTunnelFlags())
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
}

func parseTunnelFlags() tunnelConfig {
	var c tunnelConfig
	flag.StringVar(&c.provider, "provider", "", "provider executable")
	flag.StringVar(&c.host, "host", "fixture-zsh", "fixture host")
	flag.StringVar(&c.port, "port", "22", "SSH port")
	flag.StringVar(&c.builtin, "builtin", "false", "builtin transport")
	flag.StringVar(&c.dest, "dest", "/home/fixture-zsh/real-agent", "agent destination")
	flag.StringVar(&c.payload, "payload", "", "actual Linux agent")
	flag.StringVar(&c.check, "check", "true", "replacement condition")
	flag.Parse()
	return c
}

func runTunnel(config tunnelConfig) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	client, bridge := net.Pipe()
	defer func() { _ = client.Close(); _ = bridge.Close() }()
	run := &tunnelRun{config: config, bridge: bridge, completed: make(chan map[string]any, 1)}
	go func() { run.completed <- run.inject(ctx) }()
	result := map[string]any{"host": config.host, "builtin": config.builtin}
	err := run.connect(client, result)
	result["tunnelError"] = ""
	if err != nil {
		result["tunnelError"] = err.Error()
	}
	cancel()
	_ = bridge.Close()
	_ = client.Close()
	select {
	case injection := <-run.completed:
		maps.Copy(result, injection)
		result["stderr"] = run.stderr.String()
	case <-time.After(8 * time.Second):
		result["injectWaitTimeout"] = true
	}
	return result
}

func (r *tunnelRun) inject(ctx context.Context) map[string]any {
	quoted, err := inject.QuotePOSIXLiteral(r.config.dest)
	if err != nil {
		return map[string]any{"injectError": err.Error()}
	}
	ran, err := inject.Inject(ctx, inject.InjectOptions{
		Exec: r.execute, LocalFile: r.openPayload,
		ScriptParams: &inject.Params{
			AgentRemotePath: r.config.dest, DownloadURLs: &inject.DownloadURLs{},
			ExistsCheck: r.config.check, ShouldChmodPath: true,
			Command: "exec " + quoted + " internal ssh-server --stdio --log-level=error",
		},
		Stdin: r.bridge, Stdout: r.bridge, Stderr: &r.stderr, Timeout: 150 * time.Second,
	})
	message := ""
	if err != nil {
		message = err.Error()
	}
	return map[string]any{"executed": ran, "injectError": message, "localRequests": r.requests}
}

//nolint:revive // The callback must match inject.ExecFunc's five-argument signature.
func (r *tunnelRun) execute(
	ctx context.Context,
	script string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) error {
	// #nosec G204 -- This harness deliberately executes the operator-selected provider artifact.
	cmd := exec.CommandContext(ctx, r.config.provider, "command")
	cmd.Env = append(
		os.Environ(),
		"HOST="+r.config.host,
		"PORT="+r.config.port,
		"USE_BUILTIN_SSH="+r.config.builtin,
		"COMMAND="+script,
		"DOCKER_PATH=/bin/true",
		"AGENT_PATH="+r.config.dest,
	)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func (r *tunnelRun) openPayload(_ bool) (io.ReadCloser, error) {
	r.requests++
	// #nosec G304 G703 -- The operator explicitly selects the Linux agent artifact being tested.
	return os.Open(r.config.payload)
}

func (r *tunnelRun) connect(client net.Conn, result map[string]any) error {
	if err := client.SetDeadline(time.Now().Add(150 * time.Second)); err != nil {
		return err
	}
	conn, channels, requests, err := ssh.NewClientConn(client, "fixture-agent", &ssh.ClientConfig{
		User: r.config.host,
		// #nosec G106 -- The SSH server is the ephemeral agent just injected into the isolated fixture.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 150 * time.Second,
	})
	if err != nil {
		return err
	}
	sshClient := ssh.NewClient(conn, channels, requests)
	defer func() { _ = sshClient.Close() }()
	result["serverVersion"] = string(conn.ServerVersion())
	return runTunnelCommands(sshClient, result)
}

func runTunnelCommands(client *ssh.Client, result map[string]any) error {
	for index, command := range []string{"printf first-command", "printf live-followon"} {
		session, err := client.NewSession()
		if err != nil {
			return err
		}
		out, err := session.Output(command)
		_ = session.Close()
		result[fmt.Sprintf("command%d", index+1)] = string(out)
		if err != nil {
			return err
		}
	}
	return nil
}
