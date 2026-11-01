package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"strings"
	"sync"
)

type testEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

type testID struct {
	packagePath string
	name        string
}

type outputCapture struct {
	mu            sync.Mutex
	log           io.Writer
	stdout        io.Writer
	stderr        io.Writer
	stdoutPending []byte
	stderrPending []byte
	states        map[testID]testState
	listed        map[testID]struct{}
	writeErr      error
	runtimeFatal  bool
}

func newOutputCapture(log, stdout, stderr io.Writer) *outputCapture {
	return &outputCapture{
		log: log, stdout: stdout, stderr: stderr,
		states: make(map[testID]testState), listed: make(map[testID]struct{}),
	}
}

type streamWriter struct {
	capture *outputCapture
	stdout  bool
}

func (c *outputCapture) stdoutWriter() io.Writer { return streamWriter{capture: c, stdout: true} }
func (c *outputCapture) stderrWriter() io.Writer { return streamWriter{capture: c} }

func (w streamWriter) Write(p []byte) (int, error) {
	c := w.capture
	c.mu.Lock()
	defer c.mu.Unlock()

	console := c.stderr
	if w.stdout {
		console = c.stdout
	}
	if err := writeAll(console, p); err != nil {
		c.recordError(fmt.Errorf("write to console: %w", err))
	}
	c.writeArtifact(p, w.stdout)
	return len(p), nil
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		data = data[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func (c *outputCapture) writeArtifact(data []byte, stdout bool) {
	pending := &c.stderrPending
	if stdout {
		pending = &c.stdoutPending
	}
	*pending = append(*pending, data...)
	for {
		newline := bytes.IndexByte(*pending, '\n')
		if newline < 0 {
			return
		}
		line := (*pending)[:newline+1]
		c.readLine(line[:newline], stdout)
		if err := writeAll(c.log, line); err != nil {
			c.recordError(fmt.Errorf("write artifact: %w", err))
		}
		*pending = (*pending)[newline+1:]
	}
}

func (c *outputCapture) readLine(line []byte, stdout bool) {
	if stdout {
		c.readEvent(line)
		return
	}
	c.noteRuntimeFatal(string(line))
}

func (c *outputCapture) readEvent(line []byte) {
	var event testEvent
	if json.Unmarshal(bytes.TrimSpace(line), &event) != nil || event.Package == "" {
		c.noteRuntimeFatal(string(line))
		return
	}
	c.noteRuntimeFatal(event.Output)
	if event.Test == "" {
		c.readListedTests(event.Package, event.Output)
		return
	}
	id := testID{packagePath: event.Package, name: event.Test}
	c.applyTestEvent(id, event.Action)
}

func (c *outputCapture) applyTestEvent(id testID, action string) {
	state := c.states[id]
	switch action {
	case "run":
		state.started = true
	case "pass":
		state.passed = true
	case "skip":
		state.skipped = true
	case "fail":
		state.failed = true
	default:
		return
	}
	c.states[id] = state
}

func (c *outputCapture) readListedTests(packagePath, output string) {
	for line := range strings.SplitSeq(output, "\n") {
		name := strings.TrimSuffix(line, "\r")
		if isListedTestName(name) {
			c.listed[testID{packagePath: packagePath, name: name}] = struct{}{}
		}
	}
}

func isListedTestName(name string) bool {
	if !strings.HasPrefix(name, "Test") || strings.ContainsAny(name, "/ \t\r\n") {
		return false
	}
	return true
}

func (c *outputCapture) finish() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, stream := range []struct {
		pending *[]byte
		stdout  bool
	}{{&c.stdoutPending, true}, {&c.stderrPending, false}} {
		if len(*stream.pending) == 0 {
			continue
		}
		c.readLine(*stream.pending, stream.stdout)
		if err := writeAll(c.log, *stream.pending); err != nil {
			c.recordError(fmt.Errorf("write artifact: %w", err))
		}
		*stream.pending = nil
	}
}

func (c *outputCapture) noteRuntimeFatal(output string) {
	for line := range strings.SplitSeq(output, "\n") {
		if isRuntimeFatalLine(strings.TrimSuffix(line, "\r")) {
			c.runtimeFatal = true
		}
	}
}

func (c *outputCapture) sawRuntimeFatal() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runtimeFatal
}

func (c *outputCapture) events() map[testID]testState {
	c.mu.Lock()
	defer c.mu.Unlock()
	snapshot := make(map[testID]testState, len(c.states))
	maps.Copy(snapshot, c.states)
	return snapshot
}

func (c *outputCapture) listedTests() map[testID]struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	snapshot := make(map[testID]struct{}, len(c.listed))
	maps.Copy(snapshot, c.listed)
	return snapshot
}

func (c *outputCapture) err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeErr
}

func (c *outputCapture) recordError(err error) {
	if c.writeErr == nil {
		c.writeErr = err
	}
}
