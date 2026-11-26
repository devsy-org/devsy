package main

import (
	"fmt"
	"io"
	"os"
)

type Logger struct {
	Out io.Writer
	Err io.Writer
}

func newLogger() *Logger { return &Logger{Out: os.Stdout, Err: os.Stderr} }
func (l *Logger) Printf(format string, args ...any) {
	if l != nil && l.Out != nil {
		_, _ = fmt.Fprintf(l.Out, format+"\n", args...)
	}
}

func (l *Logger) Errorf(format string, args ...any) {
	if l != nil && l.Err != nil {
		_, _ = fmt.Fprintf(l.Err, format+"\n", args...)
	}
}
