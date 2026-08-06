package provider

import (
	"context"
	"fmt"
	"io"
	"net/url"

	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/platform/remotecommand"
)

// dialAndExecute finds the current workspace, dials the given sub-resource
// action on it, and streams the resulting connection through stdin/stdout/stderr.
func dialAndExecute(
	ctx context.Context,
	configPath string,
	action string,
	envFlags url.Values,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	baseClient, err := client.InitClientFromPath(ctx, configPath)
	if err != nil {
		return err
	}

	info, err := platform.GetWorkspaceInfoFromEnv()
	if err != nil {
		return err
	}
	opts := platform.FindInstanceOptions{UID: info.UID, ProjectName: info.ProjectName}
	workspace, err := platform.FindInstance(ctx, baseClient, opts)
	if err != nil {
		return err
	} else if workspace == nil {
		return fmt.Errorf("couldn't find workspace")
	}

	conn, err := platform.DialInstance(baseClient, workspace, action, envFlags)
	if err != nil {
		return err
	}

	_, err = remotecommand.ExecuteConn(ctx, conn, stdin, stdout, stderr)
	if err != nil {
		return fmt.Errorf("error executing: %w", err)
	}

	return nil
}
