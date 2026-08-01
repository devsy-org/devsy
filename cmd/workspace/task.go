package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/devsy-org/devsy/pkg/task"
	"github.com/spf13/cobra"
)

// NewTaskCmd builds the workspace task parent command.
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
		out, err := json.MarshalIndent(states, "", "  ")
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(os.Stdout, string(out))
		return nil
	}

	rows := make([][]string, 0, len(states))
	for _, s := range states {
		rows = append(rows, []string{s.ID, string(s.Status), s.Command, s.WorkspaceID})
	}
	table.Print([]string{"ID", "Status", "Command", "Workspace"}, rows)
	return nil
}

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
	if interval <= 0 {
		return fmt.Errorf("--interval must be positive, got %q", cmd.Interval)
	}
	return followTask(context.Background(), store, followTaskOptions{
		id:       id,
		interval: interval,
		emitJSON: emitJSON,
	})
}

type followTaskOptions struct {
	id       string
	interval time.Duration
	emitJSON bool
}

func followTask(ctx context.Context, store *task.Store, opts followTaskOptions) error {
	var last *task.State
	ticker := time.NewTicker(opts.interval)
	defer ticker.Stop()

	tailer := newLogTailer(opts.id)
	defer tailer.flush(os.Stderr)

	for {
		tailer.poll(os.Stderr)

		state, err := store.Get(opts.id)
		if err != nil {
			return err
		}
		emitTaskTransition(last, state, opts.emitJSON)
		last = state

		if state.Status.Terminal() {
			return reportTaskState(state, opts.emitJSON)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

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
	_, _ = fmt.Fprintf(os.Stdout, "task %s: %s\n", current.ID, current.Phase)
}

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

	// Report the raw state. Canceling is not itself a failure.
	if emitJSON {
		return json.NewEncoder(os.Stdout).Encode(state)
	}
	_, _ = fmt.Fprintf(os.Stdout, "task %s: canceled\n", state.ID)
	return nil
}

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
	emitJSON, err := resolveEmitJSON(cmd.GlobalFlags)
	if err != nil {
		return err
	}
	store, err := task.NewStore()
	if err != nil {
		return err
	}
	if cmd.Force {
		if err := store.Open(id).Cancel(); err != nil {
			return err
		}
	}
	if err := store.Delete(id, cmd.Force); err != nil {
		return err
	}

	if emitJSON {
		return json.NewEncoder(os.Stdout).Encode(struct{}{})
	}
	_, _ = fmt.Fprintf(os.Stdout, "task %s: removed\n", id)
	return nil
}

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

	_, _ = fmt.Fprintf(os.Stdout, "task %s: %s\n", state.ID, state.Status)
	if state.Status == task.StatusFailed {
		return fmt.Errorf("%s", taskErrorMessage(state))
	}
	return nil
}

// taskErrorMessage falls back to a generic message when a failed task has
// no recorded error, so callers never see a blank error.
func taskErrorMessage(state *task.State) string {
	if state.Error != "" {
		return state.Error
	}
	return fmt.Sprintf("task %s failed", state.ID)
}

func reportTaskStateJSON(state *task.State) error {
	switch state.Status {
	case task.StatusFailed:
		message := taskErrorMessage(state)
		if err := config2.WriteErrorJSON(os.Stdout, message); err != nil {
			return err
		}
		return fmt.Errorf("%s", message)
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
