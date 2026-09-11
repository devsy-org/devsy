package status

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/terminal"
)

// ReporterOptions describes the presentation selected for a status stream.
// Envelope is used only when Format resolves to JSON; keeping the encoder
// callback here avoids coupling this package to a particular wire envelope.
type ReporterOptions struct {
	Format      string
	Out         io.Writer
	Prefix      string
	Labels      map[Phase]string
	Envelope    func(Event) error
	Interactive bool
	Verbose     bool
	// SuppressFailureDetails leaves failure details to the command boundary,
	// which prevents interactive CLI commands from rendering the same error
	// once in the status stream and again in the root error renderer. Structured
	// reporters still carry the complete error.
	SuppressFailureDetails bool
}

// NewReporter selects one status presentation for a command. Selection is
// deliberately centralized so command implementations do not each grow their
// own terminal and machine-consumer rules.
func NewReporter(opts ReporterOptions) (Reporter, error) { //nolint:cyclop // centralizes the supported output-format selection matrix
	format := opts.Format
	if format == "" {
		format = "auto"
	}
	if os.Getenv("DEVSY_UI") == "true" {
		format = "json"
	}
	if format == "auto" {
		// Most command callers pass os.Stdout, which is a writer rather than a
		// reader. Use the shared terminal decision for that path; retain the
		// reader probe for test and embedding writers that expose their own fd.
		if opts.Out == os.Stdout {
			opts.Interactive = opts.Interactive || terminal.IsTerminalOut
		} else if reader, ok := opts.Out.(io.Reader); ok {
			opts.Interactive = opts.Interactive || terminal.IsTerminal(reader)
		}
		if opts.Interactive {
			format = "plain"
		} else {
			format = "json"
		}
	}

	switch format {
	case "json":
		if opts.Envelope == nil {
			return nil, fmt.Errorf("JSON status output requires an envelope encoder")
		}
		return NewEnvelopeReporter(opts.Envelope), nil
	case "plain":
		if opts.Interactive {
			return newHumanReporter(opts.Out, opts.Prefix, opts.Labels, opts.Verbose, opts.SuppressFailureDetails), nil
		}
		return newPlainReporter(opts.Out, opts.Prefix, opts.Labels, opts.Verbose, opts.SuppressFailureDetails), nil
	default:
		return nil, fmt.Errorf("unexpected status output format %q; choose json, plain, or auto", format)
	}
}

// PlainReporter renders deterministic ASCII status lines. It never writes
// terminal control sequences, making it safe for redirected output and CI.
type PlainReporter struct {
	mu                     *sync.Mutex
	out                    io.Writer
	prefix                 string
	labels                 map[Phase]string
	showDurations          bool
	suppressFailureDetails bool
	redactor               *secrets.Redactor
}

// HumanReporter is the interactive human presentation. It deliberately uses
// the same deterministic ASCII rendering as PlainReporter today; the distinct
// type leaves room for TTY-only in-place updates without changing callers.
type HumanReporter struct {
	PlainReporter
}

// NewHumanReporter creates an ASCII-safe human reporter.
func NewHumanReporter(out io.Writer, prefix string, labels map[Phase]string) Reporter {
	return newHumanReporter(out, prefix, labels, false, false)
}

func newHumanReporter(out io.Writer, prefix string, labels map[Phase]string, verbose, suppressFailureDetails bool) Reporter {
	return HumanReporter{PlainReporter: newPlainReporter(out, prefix, labels, verbose, suppressFailureDetails)}
}

// NewPlainReporter creates a human-readable status reporter. labels may be
// nil; in that case the phase name is used as-is.
func NewPlainReporter(out io.Writer, prefix string, labels map[Phase]string) Reporter {
	return newPlainReporter(out, prefix, labels, false, false)
}

func newPlainReporter(out io.Writer, prefix string, labels map[Phase]string, verbose, suppressFailureDetails bool) PlainReporter {
	return PlainReporter{
		mu:                     &sync.Mutex{},
		out:                    out,
		prefix:                 prefix,
		labels:                 labels,
		showDurations:          verbose,
		suppressFailureDetails: suppressFailureDetails,
		redactor:               secrets.NewEnvironmentRedactor(os.Environ()),
	}
}

func (r PlainReporter) Report(e Event) { //nolint:cyclop // renders the complete structured failure detail set in one record
	if r.out == nil {
		return
	}
	if r.mu != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
	}
	state := e.State
	marker := "[INFO]"
	switch state {
	case StateStarted:
		marker = "[RUN]"
	case StateSucceeded:
		marker = "[OK]"
	case StateFailed:
		marker = "[FAIL]"
	case StateSkipped:
		marker = "[SKIP]"
	}
	message := r.redact(r.message(e))
	if r.prefix != "" {
		message = r.prefix + ": " + message
	}
	duration := ""
	if e.Duration >= time.Second || (r.showDurations && e.Duration > 0) {
		duration = fmt.Sprintf(" (%.1fs)", e.Duration.Seconds())
	}
	_, _ = fmt.Fprintf(r.out, "%-6s %s%s\n", marker, message, duration)
	if state == StateFailed && e.Error != nil && !r.suppressFailureDetails {
		if text := strings.TrimSpace(r.redact(e.Error.Message)); text != "" {
			_, _ = fmt.Fprintf(r.out, "       %s\n", text)
		}
		if code := strings.TrimSpace(r.redact(e.Error.Code)); code != "" {
			_, _ = fmt.Fprintf(r.out, "       Error code: %s\n", code)
		}
		if len(e.Error.Context) > 0 {
			_, _ = fmt.Fprintln(r.out, "       Context:")
			keys := make([]string, 0, len(e.Error.Context))
			for key := range e.Error.Context {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				_, _ = fmt.Fprintf(r.out, "         %s: %s\n", r.redact(key), r.redact(e.Error.Context[key]))
			}
		}
		if hint := strings.TrimSpace(r.redact(e.Error.Hint)); hint != "" {
			_, _ = fmt.Fprintf(r.out, "       Try: %s\n", hint)
		}
	}
}

func (r PlainReporter) redact(value string) string {
	if r.redactor == nil {
		return value
	}
	return r.redactor.Redact(value)
}

func (r PlainReporter) message(e Event) string {
	if e.Step != "" {
		return e.Step
	}
	if label, ok := r.labels[e.Phase]; ok {
		return label
	}
	return string(e.Phase)
}
