package log

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/devsy-org/devsy/pkg/scanner"
)

func PipeJSONStream() (io.WriteCloser, chan struct{}) {
	done := make(chan struct{})
	reader, writer := io.Pipe()
	go func() {
		ReadJSONStream(reader)
		close(done)
	}()

	return writer, done
}

// jsonLine is a self-contained representation of a JSON log line,
// replacing the old github.com/devsy-org/log.Line type.
type jsonLine struct {
	Message string `json:"message,omitempty"`
	Msg     string `json:"msg,omitempty"`
	Level   string `json:"level,omitempty"`
}

func (l *jsonLine) text() string {
	if l.Message != "" {
		return l.Message
	}
	return l.Msg
}

var levelFuncs = map[string]func(...any){
	"trace":   Debug,
	"debug":   Debug,
	"info":    Info,
	"warning": Warn,
	"warn":    Warn,
	"error":   Error,
	"panic":   Error,
	"fatal":   Error,
}

func ReadJSONStream(reader io.Reader) {
	scan := scanner.NewScanner(reader)
	for scan.Scan() {
		line := scan.Bytes()
		if len(line) == 0 {
			continue
		}
		obj := &jsonLine{}
		if err := json.Unmarshal(line, obj); err != nil {
			continue
		}
		msg := obj.text()
		if msg == "" {
			continue
		}
		if fn, ok := levelFuncs[strings.ToLower(obj.Level)]; ok {
			fn(msg)
		}
	}
}
