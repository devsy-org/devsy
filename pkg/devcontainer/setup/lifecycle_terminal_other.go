//go:build !linux

package setup

import (
	"context"
	"errors"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/subprocess"
)

var errInteractiveUnsupported = errors.New(
	"interactive lifecycle commands are only supported in Linux containers",
)

type interactiveLaunch struct{}

func prepareInteractiveLaunch(_ context.Context, _ []string, _ string) (interactiveLaunch, error) {
	return interactiveLaunch{}, errInteractiveUnsupported
}

func runInteractiveProcess(
	_ context.Context, _ hookRunParams, _ interactiveLaunch, _ tunnel.TerminalAttachment,
) (subprocess.Result, error) {
	return subprocess.Result{ExitCode: -1}, errInteractiveUnsupported
}
