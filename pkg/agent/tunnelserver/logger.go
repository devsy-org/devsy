package tunnelserver

import (
	"context"
	"fmt"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
)

// Logger is the minimal logging interface used by workspace initialization.
// It is implemented by tunnelLogger, which sends log messages through the
// tunnel protocol back to the client.
type Logger interface {
	Debugf(format string, args ...any)
	Info(args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
}

type level int

const (
	levelWarn level = iota + 1
	levelInfo
	levelDebug
)

func NewTunnelLogger(ctx context.Context, client tunnel.TunnelClient, debug bool) Logger {
	l := levelInfo
	if debug {
		l = levelDebug
	}

	logger := &tunnelLogger{
		ctx:     ctx,
		client:  client,
		level:   l,
		logChan: make(chan *tunnel.LogMessage, 1000), // Buffer size of 1000 messages
	}

	go logger.worker()

	return logger
}

type tunnelLogger struct {
	ctx     context.Context
	level   level
	client  tunnel.TunnelClient
	logChan chan *tunnel.LogMessage
}

func (s *tunnelLogger) worker() {
	for {
		select {
		case msg := <-s.logChan:
			ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
			_, _ = s.client.Log(ctx, msg)
			// ignore error since we can't use the logger itself
			cancel()
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *tunnelLogger) Debugf(format string, args ...any) {
	if s.level < levelDebug {
		return
	}

	s.logChan <- &tunnel.LogMessage{
		LogLevel: tunnel.LogLevel_DEBUG,
		Message:  fmt.Sprintf(format, args...) + "\n",
	}
}

func (s *tunnelLogger) Info(args ...any) {
	if s.level < levelInfo {
		return
	}

	s.logChan <- &tunnel.LogMessage{
		LogLevel: tunnel.LogLevel_INFO,
		Message:  fmt.Sprintln(args...),
	}
}

func (s *tunnelLogger) Infof(format string, args ...any) {
	if s.level < levelInfo {
		return
	}

	s.logChan <- &tunnel.LogMessage{
		LogLevel: tunnel.LogLevel_INFO,
		Message:  fmt.Sprintf(format, args...) + "\n",
	}
}

func (s *tunnelLogger) Warnf(format string, args ...any) {
	if s.level < levelWarn {
		return
	}

	s.logChan <- &tunnel.LogMessage{
		LogLevel: tunnel.LogLevel_WARNING,
		Message:  fmt.Sprintf(format, args...) + "\n",
	}
}
