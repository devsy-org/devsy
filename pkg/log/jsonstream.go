package log

import (
	"io"

	"github.com/devsy-org/devsy/pkg/scanner"
)

func PipeJSONStream() (io.WriteCloser, chan struct{}) {
	done := make(chan struct{})
	reader, writer := io.Pipe()
	go func() {
		ReadJSONStream(reader)
		// closing here unblocks a Write on writer if the scanner
		// stopped early instead of at pipe close.
		_ = reader.Close()
		close(done)
	}()

	return writer, done
}

// PipeJSONStreamWithFallback is like PipeJSONStream, but a line that is not
// valid JSON is written verbatim (with a trailing newline) to fallback
// instead of being silently dropped. Use this when the writer's lifetime
// spans more than one process/phase and only some of them are known to
// produce structured JSON (e.g. a shell script's plain-text stderr
// followed by a devsy subcommand's JSON logs on the same stream).
func PipeJSONStreamWithFallback(fallback io.Writer) (io.WriteCloser, chan struct{}) {
	done := make(chan struct{})
	reader, writer := io.Pipe()
	go func() {
		readJSONStreamWithFallback(reader, fallback)
		// If the scanner stopped early (e.g. an oversized line), reader
		// otherwise stays open and a later Write on writer blocks forever
		// with nothing left reading the pipe. Closing it here unblocks the
		// paired writer with io.ErrClosedPipe instead of deadlocking.
		_ = reader.Close()
		close(done)
	}()

	return writer, done
}

func ReadJSONStream(reader io.Reader) {
	readJSONStreamWithFallback(reader, nil)
}

// readJSONStreamWithFallback parses reader line by line as JSON log lines.
// A line that isn't valid JSON, or whose level isn't recognized, is written
// to fallback verbatim if fallback is non-nil; otherwise it's dropped.
func readJSONStreamWithFallback(reader io.Reader, fallback io.Writer) {
	scan := scanner.NewScanner(reader)
	for scan.Scan() {
		line := scan.Bytes()
		if len(line) == 0 {
			continue
		}

		decoded, ok := decodeJSONLogLine(line)
		if !ok || !decoded.recognized {
			writeFallbackLine(fallback, line)
			continue
		}
		logAtZapLevel(decoded.level, decoded.text)
	}
}

func writeFallbackLine(fallback io.Writer, line []byte) {
	if fallback == nil {
		return
	}
	_, _ = fallback.Write(line)
	_, _ = fallback.Write([]byte("\n"))
}
