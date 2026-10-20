//go:build ignore

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/inject"
)

type harnessConfig struct {
	provider, host, port, builtin, dest, check, command string
	payload, input, download, scriptOut, prefix         string
	prefer                                              bool
	timeout                                             time.Duration
}

type liveOutput struct {
	out   bytes.Buffer
	once  sync.Once
	ready chan struct{}
	count int
}

func (w *liveOutput) Write(p []byte) (int, error) {
	n, err := w.out.Write(p)
	if w.out.Len() >= w.count+6 {
		w.once.Do(func() { close(w.ready) })
	}
	return n, err
}

type injectionRun struct {
	config      harnessConfig
	output      *liveOutput
	stderr, raw bytes.Buffer
	stdin       io.Reader
	expected    []byte
	requests    int
	execError   string
}

func main() {
	config := parseHarnessFlags()
	if config.scriptOut != "" {
		writeGeneratedScript(config)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), config.timeout)
	defer cancel()
	run := &injectionRun{config: config, output: &liveOutput{ready: make(chan struct{})}}
	cleanup := run.prepareInput(ctx)
	defer cleanup()
	ran, err := inject.Inject(ctx, inject.InjectOptions{
		Exec: run.execute, LocalFile: run.openPayload, ScriptParams: config.params(),
		Stdin: run.stdin, Stdout: run.output, Stderr: &run.stderr, Timeout: 5 * time.Second,
	})
	run.report(ran, err)
}

func parseHarnessFlags() harnessConfig {
	var c harnessConfig
	flag.StringVar(&c.provider, "provider", "", "provider executable")
	flag.StringVar(&c.host, "host", "fixture-bash", "fixture host alias")
	flag.StringVar(&c.port, "port", "22", "SSH port")
	flag.StringVar(&c.builtin, "builtin", "false", "builtin SSH")
	flag.StringVar(&c.dest, "dest", "/tmp/verify1621/agent", "remote destination")
	flag.StringVar(&c.check, "check", "true", "installation condition")
	flag.StringVar(&c.command, "command", "/tmp/verify1621/agent", "post-bootstrap command")
	flag.StringVar(&c.payload, "payload", "", "binary source")
	flag.StringVar(&c.input, "input", "", "4096-byte binary duplex test input")
	flag.StringVar(&c.download, "download", "", "download URL")
	flag.StringVar(&c.scriptOut, "script-out", "", "write generated script and exit")
	flag.StringVar(&c.prefix, "prefix", "", "POSIX prefix for generated script")
	flag.BoolVar(&c.prefer, "prefer-download", false, "prefer download before stdin")
	flag.DurationVar(&c.timeout, "timeout", 12*time.Second, "overall bound")
	flag.Parse()
	return c
}

func (c harnessConfig) params() *inject.Params {
	return &inject.Params{
		Command: c.command, AgentRemotePath: c.dest,
		DownloadURLs: &inject.DownloadURLs{Amd: c.download, Arm: c.download},
		ExistsCheck:  c.check, ShouldChmodPath: true, PreferAgentDownload: c.prefer,
	}
}

func writeGeneratedScript(c harnessConfig) {
	script, err := inject.GenerateScript(inject.Script, c.params())
	if err != nil {
		panic(err)
	}
	// #nosec G703 -- The operator explicitly selects the private fixture output file.
	if err = os.WriteFile(c.scriptOut, []byte(script), 0o600); err != nil {
		panic(err)
	}
}

func (r *injectionRun) prepareInput(ctx context.Context) func() {
	r.stdin = bytes.NewReader([]byte("tunnel-after-handshake\n"))
	if r.config.input == "" {
		return func() {}
	}
	// #nosec G304 G703 -- The operator explicitly selects the fixture byte input file.
	data, err := os.ReadFile(r.config.input)
	if err != nil {
		panic(err)
	}
	r.output.count = len(data)
	r.expected = append(
		append(append([]byte{}, data...), []byte("READY\n")...),
		[]byte("LIVE-OK\n")...)
	reader, writer := io.Pipe()
	r.stdin = reader
	go sendLiveInput(ctx, writer, data, r.output.ready)
	return func() { _ = reader.Close() }
}

func sendLiveInput(ctx context.Context, writer *io.PipeWriter, data []byte, ready <-chan struct{}) {
	defer func() { _ = writer.Close() }()
	if _, err := writer.Write(data); err != nil {
		return
	}
	select {
	case <-ready:
		_, _ = writer.Write([]byte("LIVE-FOLLOWON\n"))
	case <-ctx.Done():
	}
}

//nolint:revive // The callback must match inject.ExecFunc's five-argument signature.
func (r *injectionRun) execute(
	ctx context.Context,
	script string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) error {
	// #nosec G204 -- This harness deliberately executes the operator-selected provider artifact.
	cmd := exec.CommandContext(ctx, r.config.provider, "command")
	cmd.Env = append(
		os.Environ(),
		"COMMAND="+r.config.prefix+script,
		"HOST="+r.config.host,
		"PORT="+r.config.port,
		"USE_BUILTIN_SSH="+r.config.builtin,
		"DOCKER_PATH=/bin/true",
		"AGENT_PATH="+r.config.dest,
	)
	cmd.Stdin = stdin
	cmd.Stdout = io.MultiWriter(stdout, &r.raw)
	cmd.Stderr = stderr
	err := cmd.Run()
	if err != nil {
		r.execError = err.Error()
	}
	return err
}

func (r *injectionRun) openPayload(_ bool) (io.ReadCloser, error) {
	r.requests++
	// #nosec G304 G703 -- The operator explicitly selects the agent artifact being tested.
	return os.Open(r.config.payload)
}

func (r *injectionRun) report(ran bool, err error) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	result := map[string]any{
		"executed": ran, "error": message, "execError": r.execError,
		"stdout": r.output.out.String(), "stdoutBytes": r.output.out.Len(),
		"stdoutSHA256":  fmt.Sprintf("%x", sha256.Sum256(r.output.out.Bytes())),
		"duplexExact":   r.expected != nil && bytes.Equal(r.expected, r.output.out.Bytes()),
		"localRequests": r.requests, "stderr": r.stderr.String(), "rawStdout": r.raw.String(),
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
}
