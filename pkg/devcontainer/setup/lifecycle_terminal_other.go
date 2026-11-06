//go:build !linux

package setup

import (
	"context"
	"errors"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/subprocess"
)

type interactiveLaunch struct{}

func prepareInteractiveLaunch(_ []string, _ string) (interactiveLaunch, error) {
	return interactiveLaunch{}, errors.New(
		"interactive lifecycle commands are unsupported on Windows",
	)
}

func runInteractiveProcess(
	_ context.Context, _ hookRunParams, _ interactiveLaunch, _ tunnel.TerminalAttachment,
) (subprocess.Result, error) {
	return subprocess.Result{
			ExitCode: -1,
		}, errors.New(
			"interactive lifecycle commands are unsupported on Windows",
		)
}
