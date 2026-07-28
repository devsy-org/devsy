package git

import (
	"bytes"
	"io"
	"regexp"
	"strconv"

	"github.com/devsy-org/devsy/pkg/log"
)

// progressLine matches git's repeating "<label>: NN% (x/y)" progress
// updates, e.g. "Receiving objects:  47% (156/333)".
var progressLine = regexp.MustCompile(`^(.+?):\s+(\d+)% \(\d+/\d+\)`)

// progressWriter splits git's --progress output on '\r' as well as '\n'
// (git overwrites a line with '\r' rather than emitting a new one), passes
// plain informational lines to next unchanged, and logs percentage updates
// thinned to one per 10 points instead of forwarding every frame.
type progressWriter struct {
	next    io.Writer
	buf     bytes.Buffer
	lastPct map[string]int
}

func newProgressWriter(next io.Writer) *progressWriter {
	return &progressWriter{next: next, lastPct: map[string]int{}}
}

func (w *progressWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\r' || b == '\n' {
			if err := w.flush(); err != nil {
				return len(p), err
			}
			continue
		}
		w.buf.WriteByte(b)
	}
	return len(p), nil
}

func (w *progressWriter) Close() error {
	return w.flush()
}

func (w *progressWriter) flush() error {
	line := w.buf.String()
	w.buf.Reset()
	if line == "" || w.reportProgress(line) {
		return nil
	}
	_, err := w.next.Write(append([]byte(line), '\n'))
	return err
}

// reportProgress returns true if line was a percentage update (thinned or
// not), so the caller knows not to also write it to the log.
func (w *progressWriter) reportProgress(line string) bool {
	m := progressLine.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	label, pct := m[1], atoiOr(m[2], -1)
	last, seen := w.lastPct[label]
	w.lastPct[label] = pct
	if seen && pct == last {
		return true // exact duplicate frame (e.g. object count didn't change)
	}
	if pct%10 == 0 || pct == 100 {
		log.Infof("%s", line)
	}
	return true
}

func atoiOr(s string, fallback int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}
