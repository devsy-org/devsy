package cmdinternal

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	agentd "github.com/devsy-org/devsy/pkg/daemon/agent"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

// FleetServerCmd holds the fleet server cmd flags.
type FleetServerCmd struct {
	*flags.GlobalFlags

	WorkspaceID string
}

// NewFleetServerCmd creates a new fleet command.
func NewFleetServerCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &FleetServerCmd{
		GlobalFlags: flags,
	}
	fleetCmd := &cobra.Command{
		Use:   "fleet-server",
		Short: "Monitor fleet server activity",
		Args:  cobra.NoArgs,
		RunE:  cmd.Run,
	}

	cliflags.Add(
		fleetCmd,
		cliflags.String(
			&cmd.WorkspaceID,
			names.FleetWorkspaceID,
			"",
			"Fleet WorkspaceID to monitor",
		),
	)
	return fleetCmd
}

var (
	fleetFirstConnection = regexp.MustCompile(`.*Received authorization request.*`)
	fleetConnStatus      = regexp.MustCompile(`.*Notify.*`)
)

// fleetActivity is the activity writer held for the life of one Fleet session.
type fleetActivity struct {
	ctx  context.Context
	stop func()
}

func (a *fleetActivity) held() bool { return a.stop != nil }

// lost reports that the lifecycle owner dropped the registration.
func (a *fleetActivity) lost() bool { return a.held() && a.ctx.Err() != nil }

func (a *fleetActivity) acquire(parent context.Context) error {
	ctx, stop, err := agentd.StartActivityWriter(parent, agentd.DefaultActivityInterval)
	if err != nil {
		return err
	}
	a.ctx, a.stop = ctx, stop
	return nil
}

func (a *fleetActivity) release() {
	if a.stop != nil {
		a.stop()
		a.ctx, a.stop = nil, nil
	}
}

// Run runs the command logic. While a Fleet client is connected it holds an
// activity writer, which registers with a managed lifecycle owner or keeps the
// legacy activity file fresh, and releases it when the session ends.
func (c *FleetServerCmd) Run(cmd *cobra.Command, _ []string) error {
	logFile := filepath.Join(os.Getenv("HOME"), ".cache/JetBrains/Fleet/log/fleet.log")

	var activity fleetActivity
	defer activity.release()

	for {
		select {
		case <-time.After(time.Second * 10):
			if activity.lost() {
				activity.release()
			}
			switch active := fleetSessionActive(logFile); {
			case active && !activity.held():
				if err := activity.acquire(cmd.Context()); err != nil {
					log.Errorf("register fleet session activity: %v", err)
				}
			case !active:
				activity.release()
			}
		case <-cmd.Context().Done():
			// context is done - either canceled or time is up for timeout
			return nil
		}
	}
}

// fleetSessionActive reports whether the Fleet log shows a connected client.
// Before the first client connects, the connected/disconnected lines are ignored.
func fleetSessionActive(logFile string) bool {
	// #nosec G304 G703 -- fixed Fleet log path under $HOME, not caller-controlled.
	raw, err := os.ReadFile(logFile)
	if err != nil {
		return false
	}
	if len(fleetFirstConnection.FindStringSubmatch(string(raw))) == 0 {
		return false
	}
	// if our last occurrence of notify is "Notify ID connected" we have an active session
	statuses := fleetConnStatus.FindAllStringSubmatch(string(raw), -1)
	if len(statuses) == 0 {
		return false
	}
	return strings.Contains(statuses[len(statuses)-1][0], "is connected")
}
