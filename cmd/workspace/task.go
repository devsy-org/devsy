package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/status"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/task"
	"github.com/spf13/cobra"
)

// NewTaskCmd builds the 'devsy workspace task' parent command.
func NewTaskCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	taskCmd := &cobra.Command{
		Use:   "task",
		Short: "Manage background tasks (e.g. from 'up --detach')",
	}
	taskCmd.AddCommand(newTaskListCmd(globalFlags))
	taskCmd.AddCommand(newTaskGetCmd(globalFlags))
	taskCmd.AddCommand(newTaskLogsCmd(globalFlags))
	taskCmd.AddCommand(newTaskCancelCmd(globalFlags))
	taskCmd.AddCommand(newTaskRmCmd(globalFlags))
	return taskCmd
}

// --- list ---

type taskListCmd struct {
	*flags.GlobalFlags
}

func newTaskListCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &taskListCmd{GlobalFlags: globalFlags}
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List background tasks, most recently started first",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return cmd.run()
		},
	}
}

func (cmd *taskListCmd) run() error {
	emitJSON, err := resolveEmitJSON(cmd.GlobalFlags)
	if err != nil {
		return err
	}

	store, err := task.NewStore()
	if err != nil {
		return err
	}
	states, err := store.List()
	if err != nil {
		return err
	}

	if emitJSON {
		return json.NewEncoder(os.Stdout).Encode(states)
	}
	for _, s := range states {
		fmt.Printf("%s\t%s\t%s\t%s\n", s.ID, s.Status, s.Command, s.WorkspaceID)
	}
	return nil
}

// --- get ---

type taskGetCmd struct {
	*flags.GlobalFlags
}

func newTaskGetCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &taskGetCmd{GlobalFlags: globalFlags}
	return &cobra.Command{
		Use:     "get <task-id>",
		Aliases: []string{"describe", "show"},
		Short:   "Show a task's current status",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.run(args[0])
		},
	}
}

func (cmd *taskGetCmd) run(id string) error {
	emitJSON, err := resolveEmitJSON(cmd.GlobalFlags)
	if err != nil {
		return err
	}
	store, err := task.NewStore()
	if err != nil {
		return err
	}
	state, err := store.Get(id)
	if err != nil {
		return err
	}
	return reportTaskState(state, emitJSON)
}

// --- logs ---

type taskLogsCmd struct {
	*flags.GlobalFlags

	Follow   bool
	Interval string
}

func newTaskLogsCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &taskLogsCmd{GlobalFlags: globalFlags}
	logsCmd := &cobra.Command{
		Use:     "logs <task-id>",
		Aliases: []string{"attach"},
		Short:   "Show a task's status, or follow it until it finishes",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.run(args[0])
		},
	}
	cliflags.Add(logsCmd,
		cliflags.Bool(&cmd.Follow, names.Follow, false,
			"Poll until the task reaches a terminal state instead of reporting once").
			Shorthand("f"),
		cliflags.String(&cmd.Interval, names.Interval, "500ms",
			"Poll interval when --follow is set"),
	)
	return logsCmd
}

func (cmd *taskLogsCmd) run(id string) error {
	emitJSON, err := resolveEmitJSON(cmd.GlobalFlags)
	if err != nil {
		return err
	}
	store, err := task.NewStore()
	if err != nil {
		return err
	}

	if !cmd.Follow {
		state, err := store.Get(id)
		if err != nil {
			return err
		}
		return reportTaskState(state, emitJSON)
	}

	interval, err := time.ParseDuration(cmd.Interval)
	if err != nil {
		return fmt.Errorf("parse --interval: %w", err)
	}
	return followTask(context.Background(), store, id, interval, emitJSON)
}

func followTask(
	ctx context.Context,
	store *task.Store,
	id string,
	interval time.Duration,
	emitJSON bool,
) error {
	var last *task.State
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		state, err := store.Get(id)
		if err != nil {
			return err
		}
		emitTaskTransition(last, state, emitJSON)
		last = state

		if state.Status.Terminal() {
			return reportTaskState(state, emitJSON)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// emitTaskTransition prints a line only when phase/step changed since the
// last poll.
func emitTaskTransition(last, current *task.State, emitJSON bool) {
	if last != nil && last.Phase == current.Phase && last.Step == current.Step {
		return
	}
	if current.Phase == "" {
		return
	}
	event := status.Event{Phase: status.Phase(current.Phase), Step: current.Step, Started: true}
	if emitJSON {
		_ = config2.WriteStatusJSON(os.Stdout, event)
		return
	}
	fmt.Printf("task %s: %s\n", current.ID, current.Phase)
}

// --- cancel ---

type taskCancelCmd struct {
	*flags.GlobalFlags
}

func newTaskCancelCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &taskCancelCmd{GlobalFlags: globalFlags}
	return &cobra.Command{
		Use:     "cancel <task-id>",
		Aliases: []string{"stop"},
		Short:   "Stop a task's process and mark it failed",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.run(args[0])
		},
	}
}

func (cmd *taskCancelCmd) run(id string) error {
	emitJSON, err := resolveEmitJSON(cmd.GlobalFlags)
	if err != nil {
		return err
	}
	store, err := task.NewStore()
	if err != nil {
		return err
	}
	if err := store.Open(id).Cancel(); err != nil {
		return err
	}
	state, err := store.Get(id)
	if err != nil {
		return err
	}

	// Report the raw state rather than routing through reportTaskState,
	// which would turn state.Error (the task is now Failed) into this
	// command's exit code — canceling successfully isn't itself a failure.
	if emitJSON {
		return json.NewEncoder(os.Stdout).Encode(state)
	}
	fmt.Printf("task %s: canceled\n", state.ID)
	return nil
}

// --- rm ---

type taskRmCmd struct {
	*flags.GlobalFlags

	Force bool
}

func newTaskRmCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &taskRmCmd{GlobalFlags: globalFlags}
	rmCmd := &cobra.Command{
		Use:     "rm <task-id>",
		Aliases: []string{"delete", "remove"},
		Short:   "Delete a finished task's state",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.run(args[0])
		},
	}
	cliflags.Add(rmCmd,
		cliflags.Bool(&cmd.Force, names.Force, false,
			"Stop the task first if it's still pending or running, then delete"),
	)
	return rmCmd
}

func (cmd *taskRmCmd) run(id string) error {
	store, err := task.NewStore()
	if err != nil {
		return err
	}
	if cmd.Force {
		if err := store.Open(id).Cancel(); err != nil {
			return err
		}
	}
	return store.Delete(id, cmd.Force)
}

// --- shared ---

func resolveEmitJSON(g *flags.GlobalFlags) (bool, error) {
	mode, err := output.ResolveMode(g.ResultFormat)
	if err != nil {
		return false, err
	}
	return mode == output.ModeJSON, nil
}

func reportTaskState(state *task.State, emitJSON bool) error {
	if emitJSON {
		return reportTaskStateJSON(state)
	}

	fmt.Printf("task %s: %s\n", state.ID, state.Status)
	if state.Status == task.StatusFailed {
		return fmt.Errorf("%s", state.Error)
	}
	return nil
}

func reportTaskStateJSON(state *task.State) error {
	switch state.Status {
	case task.StatusFailed:
		if err := config2.WriteErrorJSON(os.Stdout, state.Error); err != nil {
			return err
		}
		return fmt.Errorf("%s", state.Error)
	case task.StatusSucceeded:
		return config2.WriteResultJSON(os.Stdout, resultEnvelopeFrom(state))
	default:
		return config2.WriteStatusJSON(os.Stdout, status.Event{
			Phase:   status.Phase(state.Phase),
			Step:    state.Step,
			Started: true,
		})
	}
}

func resultEnvelopeFrom(state *task.State) config2.ResultEnvelope {
	if state.Result == nil {
		return config2.ResultEnvelope{}
	}
	return config2.ResultEnvelope{
		ContainerID: config2.GetContainerID(state.Result),
		RemoteUser:  config2.GetRemoteUser(state.Result),
		Warnings:    state.Result.HostWarnings,
		Recovery:    state.Result.RecoveryContainer,
	}
}
