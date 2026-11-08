package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/platformclient"
)

// lifecycleAction is one asynchronous request to the gateway: it is accepted
// right away and the workspace follows in the background.
type lifecycleAction struct {
	// requested is the past-tense label printed on success, e.g. "Sleep requested".
	requested string
	// gerund names the action in errors, e.g. "sleeping".
	gerund string
	call   func(*platformclient.Client, context.Context, string, string) (platformclient.Workspace, error)
}

var (
	sleepAction = lifecycleAction{
		requested: "Sleep requested",
		gerund:    "sleeping",
		call:      (*platformclient.Client).SleepWorkspace,
	}
	wakeAction = lifecycleAction{
		requested: "Wake requested",
		gerund:    "waking",
		call:      (*platformclient.Client).WakeWorkspace,
	}
	deleteAction = lifecycleAction{
		requested: "Delete requested",
		gerund:    "deleting",
		call:      (*platformclient.Client).DeleteWorkspace,
	}
)

var errNotLoggedIn = errors.New(
	"not logged in to a platform gateway: run 'devsy login <gateway-url>' first",
)

var errLoggedOut = errors.New(
	"authentication required: you are logged out of the platform gateway; " +
		"run 'devsy login <gateway-url>' to sign in again " +
		"(use --skip-platform to list local workspaces)",
)

// lifecycleTarget is what a lifecycle request needs: the gateway login, where
// to print, and how.
type lifecycleTarget struct {
	creds *config.GatewayCredentials
	out   io.Writer
	mode  string
	// ignoreNotFound treats a 404 as success, for delete --ignore-not-found.
	ignoreNotFound bool
}

// runPlatformLifecycle sends the action for every name and keeps going after a
// failure, so one bad name does not hide the others. It returns every failure.
func runPlatformLifecycle(
	ctx context.Context,
	target lifecycleTarget,
	action lifecycleAction,
	names []string,
) error {
	client := newPlatformClient(target.creds)
	var errs []error
	for _, name := range names {
		ws, err := action.call(client, ctx, target.creds.APIKey, name)
		if err != nil {
			if target.ignoreNotFound && isNotFound(err) {
				continue
			}
			errs = append(errs, fmt.Errorf("%s workspace %q: %w", action.gerund, name, err))
			continue
		}
		if err := printLifecycleResult(target, action, ws); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func isNotFound(err error) bool {
	var apiErr *platformclient.APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound
}

func printLifecycleResult(
	target lifecycleTarget,
	action lifecycleAction,
	ws platformclient.Workspace,
) error {
	if target.mode == output.ModeJSON {
		return writePlatformJSON(target.out, ws)
	}
	_, err := fmt.Fprintf(target.out, "%s for workspace %s (phase: %s)\n",
		action.requested, ws.Name, ws.Phase)
	return err
}

// resolveLifecycleTarget loads the gateway login. It fails when there is none.
func resolveLifecycleTarget(g *flags.GlobalFlags, out io.Writer) (lifecycleTarget, error) {
	devsyConfig, err := config.LoadConfig(g.Context, g.Provider)
	if err != nil {
		return lifecycleTarget{}, err
	}
	creds := platformCredentials(devsyConfig)
	if creds == nil {
		return lifecycleTarget{}, errNotLoggedIn
	}
	return newLifecycleTarget(g, creds, out)
}

func newLifecycleTarget(
	g *flags.GlobalFlags,
	creds *config.GatewayCredentials,
	out io.Writer,
) (lifecycleTarget, error) {
	mode, err := output.ResolveMode(g.ResultFormat)
	if err != nil {
		return lifecycleTarget{}, err
	}
	if out == nil {
		out = os.Stdout
	}
	return lifecycleTarget{creds: creds, out: out, mode: mode}, nil
}
