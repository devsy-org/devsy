package log

import (
	"encoding/json"
	"io"

	"github.com/devsy-org/devsy/pkg/scanner"
	"github.com/devsy-org/log"
	"github.com/sirupsen/logrus"
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

var levelFuncs = map[logrus.Level]func(...any){
	logrus.TraceLevel: Debug,
	logrus.DebugLevel: Debug,
	logrus.InfoLevel:  Info,
	logrus.WarnLevel:  Warn,
	logrus.ErrorLevel: Error,
	logrus.PanicLevel: Error,
	logrus.FatalLevel: Error,
}

func ReadJSONStream(reader io.Reader) {
	scan := scanner.NewScanner(reader)
	for scan.Scan() {
		lineObject, err := Unmarshal(scan.Bytes())
		if err == nil && lineObject.Message != "" {
			if fn, ok := levelFuncs[lineObject.Level]; ok {
				fn(lineObject.Message)
			}
		}
	}
}

func Unmarshal(line []byte) (*log.Line, error) {
	lineObject := &log.Line{}
	err := json.Unmarshal(line, lineObject)
	if err != nil {
		return nil, err
	}

	return lineObject, nil
}
