package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"sync"
)

type testEvent struct {
	Action string
	Test   string
}

type outputCapture struct {
	mu            sync.Mutex
	log           io.Writer
	stdout        io.Writer
	stderr        io.Writer
	stdoutPending []byte
	stderrPending []byte
	states        map[string]testState
	writeErr      error
}

func newOutputCapture(log, stdout, stderr io.Writer) *outputCapture {
	return &outputCapture{
		log: log, stdout: stdout, stderr: stderr, states: make(map[string]testState),
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
		if stdout {
			c.readEvent(line[:newline])
		}
		if err := writeAll(c.log, line); err != nil {
			c.recordError(fmt.Errorf("write artifact: %w", err))
		}
		*pending = (*pending)[newline+1:]
	}
}

func (c *outputCapture) readEvent(line []byte) {
	var event testEvent
	if json.Unmarshal(bytes.TrimSpace(line), &event) != nil || event.Test == "" {
		return
	}
	state := c.states[event.Test]
	switch event.Action {
	case "pass":
		state.passed = true
	case "skip":
		state.skipped = true
	case "fail":
		state.failed = true
	}
	c.states[event.Test] = state
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
		if stream.stdout {
			c.readEvent(*stream.pending)
		}
		if err := writeAll(c.log, *stream.pending); err != nil {
			c.recordError(fmt.Errorf("write artifact: %w", err))
		}
		*stream.pending = nil
	}
}

func (c *outputCapture) events() map[string]testState {
	c.mu.Lock()
	defer c.mu.Unlock()
	copy := make(map[string]testState, len(c.states))
	maps.Copy(copy, c.states)
	return copy
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
