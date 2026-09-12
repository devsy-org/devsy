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

const (
	formatAuto  = "auto"
	formatJSON  = "json"
	formatPlain = "plain"
)

// NewReporter selects one status presentation for a command. Selection is
// deliberately centralized so command implementations do not each grow their
// own terminal and machine-consumer rules.
func NewReporter(opts ReporterOptions) (Reporter, error) {
	return newReporter(resolveFormat(opts), opts)
}

func resolveFormat(opts ReporterOptions) string {
	format := opts.Format
	if format == "" {
		format = formatAuto
	}
	if os.Getenv("DEVSY_UI") == "true" {
		return formatJSON
	}
	if format != formatAuto {
		return format
	}
	return resolveAutomaticFormat(opts)
}

func resolveAutomaticFormat(opts ReporterOptions) string {
	if opts.Out == os.Stdout {
		if terminal.IsTerminalOut {
			return formatPlain
		}
		return formatJSON
	}
	if reader, ok := opts.Out.(io.Reader); ok && terminal.IsTerminal(reader) {
		return formatPlain
	}
	if opts.Interactive {
		return formatPlain
	}
	return formatJSON
}

func newReporter(format string, opts ReporterOptions) (Reporter, error) {
	switch format {
	case formatJSON:
		if opts.Envelope == nil {
			return nil, fmt.Errorf("JSON status output requires an envelope encoder")
		}
		return NewEnvelopeReporter(opts.Envelope), nil
	case formatPlain:
		config := reporterConfig{
			out:                    opts.Out,
			prefix:                 opts.Prefix,
			labels:                 opts.Labels,
			verbose:                opts.Verbose,
			suppressFailureDetails: opts.SuppressFailureDetails,
		}
		if opts.Interactive {
			return newHumanReporter(config), nil
		}
		return newPlainReporter(config), nil
	default:
		return nil, fmt.Errorf(
			"unexpected status output format %q; choose json, plain, or auto",
			format,
		)
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
	return newHumanReporter(reporterConfig{out: out, prefix: prefix, labels: labels})
}

type reporterConfig struct {
	out                    io.Writer
	prefix                 string
	labels                 map[Phase]string
	verbose                bool
	suppressFailureDetails bool
}

func newHumanReporter(config reporterConfig) Reporter {
	return HumanReporter{PlainReporter: newPlainReporter(config)}
}

// NewPlainReporter creates a human-readable status reporter. labels may be
// nil; in that case the phase name is used as-is.
func NewPlainReporter(out io.Writer, prefix string, labels map[Phase]string) Reporter {
	return newPlainReporter(reporterConfig{out: out, prefix: prefix, labels: labels})
}

func newPlainReporter(config reporterConfig) PlainReporter {
	return PlainReporter{
		mu:                     &sync.Mutex{},
		out:                    config.out,
		prefix:                 config.prefix,
		labels:                 config.labels,
		showDurations:          config.verbose,
		suppressFailureDetails: config.suppressFailureDetails,
		redactor:               secrets.NewEnvironmentRedactor(os.Environ()),
	}
}

func (r PlainReporter) Report(e Event) {
	if r.out == nil {
		return
	}
	if r.mu != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
	}
	marker := eventMarker(e.State)
	message := r.redact(r.message(e))
	if r.prefix != "" {
		message = r.prefix + ": " + message
	}
	duration := eventDuration(e.Duration, r.showDurations)
	_, _ = fmt.Fprintf(r.out, "%-6s %s%s\n", marker, message, duration)
	if e.State == StateFailed && e.Error != nil && !r.suppressFailureDetails {
		r.reportFailureDetails(e.Error)
	}
}

func eventMarker(state State) string {
	switch state {
	case StateStarted:
		return "[RUN]"
	case StateSucceeded:
		return "[OK]"
	case StateFailed:
		return "[FAIL]"
	case StateSkipped:
		return "[SKIP]"
	default:
		return "[INFO]"
	}
}

func eventDuration(duration time.Duration, showDurations bool) string {
	if duration < time.Second && (!showDurations || duration <= 0) {
		return ""
	}
	return fmt.Sprintf(" (%.1fs)", duration.Seconds())
}

func (r PlainReporter) reportFailureDetails(info *ErrorInfo) {
	if text := strings.TrimSpace(r.redact(info.Message)); text != "" {
		_, _ = fmt.Fprintf(r.out, "       %s\n", text)
	}
	if code := strings.TrimSpace(r.redact(info.Code)); code != "" {
		_, _ = fmt.Fprintf(r.out, "       Error code: %s\n", code)
	}
	if len(info.Context) > 0 {
		_, _ = fmt.Fprintln(r.out, "       Context:")
		keys := make([]string, 0, len(info.Context))
		for key := range info.Context {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			_, _ = fmt.Fprintf(
				r.out,
				"         %s: %s\n",
				r.redact(key),
				r.redact(info.Context[key]),
			)
		}
	}
	if hint := strings.TrimSpace(r.redact(info.Hint)); hint != "" {
		_, _ = fmt.Fprintf(r.out, "       Try: %s\n", hint)
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
