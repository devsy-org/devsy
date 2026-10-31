package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
	clientpkg "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"github.com/spf13/cobra"
)

const (
	cmdInternal     = "internal"
	cmdWorkspace    = "workspace"
	sourceContainer = "container"
	sourceSetup     = "setup"
	runLatest       = "latest"

	flagList       = "list"
	flagRun        = "run"
	flagHook       = "hook"
	flagInvocation = "invocation"
	flagAfter      = "after"
	flagLimit      = "limit"

	// refreshDeadline bounds one read-only refresh from the workspace.
	refreshDeadline = 5 * time.Second
	// minPollGap is the least time between completed polls in follow mode.
	minPollGap = 500 * time.Millisecond
	// maxRefreshPages bounds the pages one refresh may import.
	maxRefreshPages = 16
	// maxRefreshFailures ends follow after repeated failed refreshes.
	maxRefreshFailures = 3
	// maxRemoteStderr bounds the diagnostics kept from the remote reader.
	maxRemoteStderr = 4 * 1024

	reasonNoWorkspace = "no_workspace"
)

// setupLogsFlags are the flags that only apply to --source setup.
type setupLogsFlags struct {
	Source     string
	List       bool
	RunID      string
	Hook       string
	Invocation string
	Follow     bool
	After      string
	Limit      int
}

func registerSetupLogsFlags(c *cobra.Command, f *setupLogsFlags) {
	cliflags.Add(c,
		cliflags.String(
			&f.Source,
			names.Source,
			sourceContainer,
			"What to read: container (runtime logs) or setup (retained build and lifecycle output)",
		),
		cliflags.Bool(&f.List, flagList, false,
			"List retained setup runs, newest first (--source setup)"),
		cliflags.String(&f.RunID, flagRun, "",
			"Run ID or latest (--source setup, default latest for a workspace)"),
		cliflags.String(&f.Hook, flagHook, "",
			"Only this lifecycle hook, such as postCreateCommand (--source setup)"),
		cliflags.String(&f.Invocation, flagInvocation, "",
			"Only this invocation ID (--source setup)"),
		cliflags.Bool(&f.Follow, names.Follow, false,
			"Keep reading the run until it completes (--source setup)").Shorthand("f"),
		cliflags.String(&f.After, flagAfter, "",
			"Cursor from the previous page (--source setup)"),
		cliflags.Int(&f.Limit, flagLimit, 0,
			"Most records per page, or runs with --list (--source setup)"),
	)
}

// validateSource rejects bad or conflicting selectors before any I/O.
func (cmd *LogsCmd) validateSource(args []string) error {
	switch cmd.Source {
	case "", sourceContainer:
		return cmd.rejectSetupOnlyFlags()
	case sourceSetup:
		return cmd.validateSetup(args)
	default:
		return fmt.Errorf("unknown --source %q, choose container or setup", cmd.Source)
	}
}

func (cmd *LogsCmd) rejectSetupOnlyFlags() error {
	for flag, set := range map[string]bool{
		flagList: cmd.List, flagRun: cmd.RunID != "", flagHook: cmd.Hook != "",
		flagInvocation: cmd.Invocation != "", names.Follow: cmd.Follow,
		flagAfter: cmd.After != "", flagLimit: cmd.Limit != 0,
	} {
		if set {
			return fmt.Errorf("--%s requires --source setup", flag)
		}
	}

	return nil
}

func (cmd *LogsCmd) validateSetup(args []string) error {
	if err := cmd.validateShape(args); err != nil {
		return err
	}
	if _, err := output.ResolveMode(cmd.ResultFormat); err != nil {
		return err
	}
	if cmd.List {
		return validateIndexLimit(cmd.Limit)
	}

	return workspacelogs.ValidateReadSelectors(cmd.readRequest(cmd.RunID))
}

// validateShape checks the argument count and selector combinations.
func (cmd *LogsCmd) validateShape(args []string) error {
	if len(args) > 1 {
		return errors.New("at most one workspace argument is allowed with --source setup")
	}
	if err := cmd.validateListConflicts(); err != nil {
		return err
	}
	if len(args) == 0 && !cmd.List && (cmd.RunID == "" || cmd.RunID == runLatest) {
		return errors.New("without a workspace argument, --run must be an explicit run ID")
	}

	return nil
}

func (cmd *LogsCmd) validateListConflicts() error {
	if !cmd.List {
		return nil
	}
	for flag, set := range map[string]bool{
		flagRun: cmd.RunID != "", flagHook: cmd.Hook != "",
		flagInvocation: cmd.Invocation != "", names.Follow: cmd.Follow,
	} {
		if set {
			return fmt.Errorf("--%s cannot be combined with --%s", flagList, flag)
		}
	}

	return nil
}

func validateIndexLimit(limit int) error {
	if limit < 0 || limit > workspacelogs.MaxIndexLimit {
		return fmt.Errorf(
			"--limit must be between 1 and %d with --list",
			workspacelogs.MaxIndexLimit,
		)
	}

	return nil
}

// readRequest builds the page request. A run of latest is validated later, once
// it resolves to an ID.
func (cmd *LogsCmd) readRequest(runID string) workspacelogs.ReadRequest {
	if runID == "" || runID == runLatest {
		runID = workspacelogs.NewID()
	}

	return workspacelogs.ReadRequest{
		RunID: runID, Hook: cmd.Hook, InvocationID: cmd.Invocation,
		After: cmd.After, Limit: cmd.Limit,
	}
}

// setupTarget is the verified identity a read is bound to.
type setupTarget struct {
	context     string
	workspaceID string
	uid         string
	// client is nil when no supported connection exists.
	client clientpkg.WorkspaceClient
	// named is true when the caller passed a workspace argument.
	named bool
}

func (cmd *LogsCmd) runSetup(ctx context.Context, args []string) error {
	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}
	store, err := workspacelogs.OpenDefault()
	if err != nil {
		return fmt.Errorf("open workspace logs: %w", err)
	}
	explicit := cmd.RunID != "" && cmd.RunID != runLatest
	target, err := cmd.resolveTarget(ctx, devsyConfig, args, explicit)
	if err != nil {
		return err
	}
	r := &setupReader{
		store: store, json: mode == output.ModeJSON, out: os.Stdout, errOut: os.Stderr,
		refresher: newRefresher(target), flags: cmd.setupLogsFlags,
	}

	return r.run(ctx, target)
}

// resolveTarget finds the workspace identity. A retained explicit run stays
// readable when the workspace itself is gone.
func (cmd *LogsCmd) resolveTarget(
	ctx context.Context, devsyConfig *config.Config, args []string, explicit bool,
) (setupTarget, error) {
	target := setupTarget{context: devsyConfig.DefaultContext}
	if len(args) == 0 {
		return target, nil
	}
	target.named, target.workspaceID = true, args[0]
	base, err := workspace.Get(ctx, workspace.GetOptions{
		DevsyConfig: devsyConfig, Args: args, Owner: cmd.Owner,
	})
	if err != nil {
		if explicit {
			return target, nil
		}

		return target, fmt.Errorf("get workspace for setup logs: %w", err)
	}
	target.context, target.workspaceID = base.Context(), base.Workspace()
	if wc := base.WorkspaceConfig(); wc != nil {
		target.uid = wc.UID
	}
	target.client, _ = base.(clientpkg.WorkspaceClient)

	return target, nil
}

// refresher imports missing records from the workspace. It returns an
// availability reason, or empty when the refresh succeeded.
type refresher interface {
	refresh(ctx context.Context, store *workspacelogs.Store, runID string) string
}

type noRefresher struct{ reason string }

func (n noRefresher) refresh(context.Context, *workspacelogs.Store, string) string {
	return n.reason
}

func newRefresher(t setupTarget) refresher {
	switch {
	case t.client == nil && t.named:
		return noRefresher{reason: workspacelogs.ReasonRefreshUnsupported}
	case t.client == nil:
		return noRefresher{reason: reasonNoWorkspace}
	default:
		return &agentRefresher{client: t.client, context: t.context, uid: t.uid}
	}
}

// setupReader reads local history, refreshes it, and prints it.
type setupReader struct {
	store     *workspacelogs.Store
	json      bool
	out       io.Writer
	errOut    io.Writer
	refresher refresher
	flags     setupLogsFlags
}

func (r *setupReader) run(ctx context.Context, t setupTarget) error {
	if r.flags.List {
		return r.list(t)
	}
	runID, err := r.resolveRun(t)
	if err != nil {
		return err
	}
	if r.flags.Follow {
		return r.follow(ctx, runID)
	}

	return r.snapshot(ctx, runID)
}

func (r *setupReader) request(runID, after string) workspacelogs.ReadRequest {
	return workspacelogs.ReadRequest{
		RunID: runID, Hook: r.flags.Hook, InvocationID: r.flags.Invocation,
		After: after, Limit: r.flags.Limit,
	}
}

// resolveRun resolves latest once and checks the run belongs to the target.
func (r *setupReader) resolveRun(t setupTarget) (string, error) {
	runID := r.flags.RunID
	if runID == "" || runID == runLatest {
		return r.latestRun(t)
	}
	m, err := r.store.Manifest(runID)
	if err != nil {
		return "", runLookupError(err, runID)
	}

	return runID, checkRunBelongsTo(m, t)
}

func (r *setupReader) latestRun(t setupTarget) (string, error) {
	if t.uid == "" {
		return "", errors.New(
			"cannot resolve the latest run without a workspace; pass --run with a run ID",
		)
	}
	resp, err := r.store.ListRuns(workspacelogs.IndexRequest{
		Context: t.context, WorkspaceUID: t.uid, Limit: 1,
	})
	if err != nil {
		return "", err
	}
	if len(resp.Runs) == 0 {
		return "", fmt.Errorf("no retained setup runs for workspace %q", t.workspaceID)
	}

	return resp.Runs[0].RunID, nil
}

func runLookupError(err error, runID string) error {
	if errors.Is(err, workspacelogs.ErrNotFound) {
		return fmt.Errorf("no retained setup run %q in this context", runID)
	}

	return err
}

// checkRunBelongsTo matches the stored identity against the workspace the
// caller named. A display name alone never binds an old run to a new workspace.
func checkRunBelongsTo(m *workspacelogs.RunManifest, t setupTarget) error {
	if m.Context != t.context {
		return fmt.Errorf("run %s belongs to another context", m.RunID)
	}
	if t.named && !sameWorkspace(m, t) {
		return fmt.Errorf("run %s belongs to another workspace", m.RunID)
	}

	return nil
}

// sameWorkspace compares UIDs when both sides have one. Otherwise it falls back
// to the display ID the run started with.
func sameWorkspace(m *workspacelogs.RunManifest, t setupTarget) bool {
	if m.WorkspaceUID != nil && t.uid != "" {
		return *m.WorkspaceUID == t.uid
	}
	if m.WorkspaceUID == nil && m.WorkspaceIDAtStart == "" {
		return true
	}

	return m.WorkspaceIDAtStart == t.workspaceID
}

// refreshUnlessComplete runs one refresh for a run that may still be missing
// records. A completed mirrored run needs no provider access.
func (r *setupReader) refreshUnlessComplete(ctx context.Context, runID string) string {
	m, err := r.store.Manifest(runID)
	if err != nil || m.Complete() {
		return ""
	}

	return r.refresher.refresh(ctx, r.store, runID)
}

func (r *setupReader) snapshot(ctx context.Context, runID string) error {
	reason := r.refreshUnlessComplete(ctx, runID)
	if r.json {
		resp, err := r.store.Read(r.request(runID, r.flags.After))
		if err != nil {
			return err
		}
		addRefreshAvailability(resp, reason)

		return writeJSONLine(r.out, resp)
	}

	return r.plainSnapshot(runID, reason)
}

func (r *setupReader) plainSnapshot(runID, reason string) error {
	pw := &plainWriter{out: r.out, errOut: r.errOut}
	after := r.flags.After
	for {
		resp, err := r.store.Read(r.request(runID, after))
		if err != nil {
			return err
		}
		pw.page(resp)
		if !resp.HasMore {
			pw.finish(resp, reason)

			return nil
		}
		after = resp.NextCursor
	}
}

// follow drains the run, then polls it. It never reruns anything: a refresh
// only reads the workspace.
func (r *setupReader) follow(ctx context.Context, runID string) error {
	pw := &plainWriter{out: r.out, errOut: r.errOut}
	after, failures := r.flags.After, 0
	var lastPoll time.Time
	for {
		reason := r.pollRefresh(ctx, runID, &lastPoll)
		if ctx.Err() != nil {
			return nil
		}
		resp, next, err := r.drain(runID, after, pw)
		if err != nil {
			return err
		}
		after = next
		failures = nextFailures(failures, reason)
		if done, stop := r.followDone(resp, reason, failures); done {
			r.finishFollow(pw, resp, stop)

			return nil
		}
	}
}

// pollRefresh waits out the minimum gap, then refreshes without overlap.
func (r *setupReader) pollRefresh(ctx context.Context, runID string, last *time.Time) string {
	if wait := minPollGap - time.Since(*last); !last.IsZero() && wait > 0 {
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(wait):
		}
	}
	reason := r.refreshUnlessComplete(ctx, runID)
	*last = time.Now()

	return reason
}

func nextFailures(failures int, reason string) int {
	if reason == workspacelogs.ReasonRefreshFailed {
		return failures + 1
	}

	return 0
}

// drain reads every page available now and prints it.
func (r *setupReader) drain(
	runID, after string, pw *plainWriter,
) (*workspacelogs.ReadResponse, string, error) {
	for {
		resp, err := r.store.Read(r.request(runID, after))
		if err != nil {
			return nil, after, err
		}
		after = resp.NextCursor
		if r.json && len(resp.Records) > 0 {
			if err = writeJSONLine(r.out, resp); err != nil {
				return nil, after, err
			}
		} else if !r.json {
			pw.page(resp)
		}
		if !resp.HasMore {
			return resp, after, nil
		}
	}
}

// followDone decides whether to stop. The second result is the availability
// reason to report when the stop is not a completed run.
func (r *setupReader) followDone(
	resp *workspacelogs.ReadResponse, reason string, failures int,
) (bool, string) {
	if resp.Complete {
		return true, ""
	}
	switch reason {
	case workspacelogs.ReasonSourceOffline, workspacelogs.ReasonNoSpool:
		return true, reason
	}
	if failures >= maxRefreshFailures {
		return true, workspacelogs.ReasonRefreshFailed
	}
	if m, err := r.store.Manifest(resp.Run.RunID); err == nil && !m.Active() {
		return true, reason
	}

	return false, ""
}

func (r *setupReader) finishFollow(
	pw *plainWriter,
	resp *workspacelogs.ReadResponse,
	reason string,
) {
	addRefreshAvailability(resp, reason)
	if r.json {
		resp.Records = []workspacelogs.Record{}
		_ = writeJSONLine(r.out, resp)

		return
	}
	pw.finish(resp, reason)
}

func addRefreshAvailability(resp *workspacelogs.ReadResponse, reason string) {
	if reason == "" {
		return
	}
	resp.Availability = append(resp.Availability,
		workspacelogs.SourceAvailability{Available: false, Reason: reason})
	resp.Complete = false
}

func (r *setupReader) list(t setupTarget) error {
	resp, err := r.store.ListRuns(workspacelogs.IndexRequest{
		Context: t.context, WorkspaceUID: t.uid, After: r.flags.After, Limit: r.flags.Limit,
	})
	if err != nil {
		return err
	}
	if r.json {
		return writeJSONLine(r.out, resp)
	}
	for i := range resp.Runs {
		run := &resp.Runs[i]
		_, _ = fmt.Fprintf(r.out, "%s  %s  %s  command=%s  capture=%s  workspace=%s\n",
			run.RunID, run.CreatedAt.Local().Format(time.RFC3339), run.Command,
			run.CommandState, run.CaptureState, run.WorkspaceIDAtStart)
	}
	if resp.HasMore {
		_, _ = fmt.Fprintf(r.errOut, "more runs: --after %s\n", resp.NextCursor)
	}

	return nil
}

// agentRefresher imports missing records through the installed agent on the
// workspace host. The reader there only execs into a running workload.
type agentRefresher struct {
	client  clientpkg.WorkspaceClient
	context string
	uid     string
}

func (a *agentRefresher) refresh(
	ctx context.Context, store *workspacelogs.Store, runID string,
) string {
	ctx, cancel := context.WithTimeout(ctx, refreshDeadline)
	defer cancel()
	for range maxRefreshPages {
		more, reason := a.refreshPage(ctx, store, runID)
		if reason != "" || !more {
			return reason
		}
	}

	return ""
}

func (a *agentRefresher) refreshPage(
	ctx context.Context, store *workspacelogs.Store, runID string,
) (bool, string) {
	pos, err := store.LocalPositions(runID)
	if err != nil {
		return false, workspacelogs.ReasonRefreshFailed
	}
	token, err := workspacelogs.PositionCursor(runID, pos)
	if err != nil {
		return false, workspacelogs.ReasonRefreshFailed
	}
	resp, reason := a.fetch(ctx, runID, token)
	if reason != "" {
		return false, reason
	}
	res, err := store.ImportRemote(workspacelogs.ImportRequest{
		RunID: runID, Context: a.context, WorkspaceUID: a.uid, Response: resp,
	})
	if err != nil {
		return false, workspacelogs.ReasonRefreshFailed
	}

	return resp.HasMore && res.Written > 0 && len(res.Stalled) == 0, ""
}

func (a *agentRefresher) fetch(
	ctx context.Context, runID, after string,
) (*workspacelogs.ReadResponse, string) {
	stdout := &workspacelogs.BoundedBuffer{Max: workspacelogs.MaxPageBytes + 1}
	stderr := &workspacelogs.BoundedBuffer{Max: maxRemoteStderr}
	err := a.client.Command(ctx, clientpkg.CommandOptions{
		Command:   a.command(runID, after),
		Stdout:    stdout,
		Stderr:    stderr,
		RawStdout: true,
	})
	if err != nil {
		return nil, classifyRemoteFailure(stderr.String())
	}
	resp, err := workspacelogs.DecodeReadResponse(strings.NewReader(stdout.String()))
	if err != nil {
		return nil, workspacelogs.ReasonRefreshFailed
	}

	return resp, ""
}

func (a *agentRefresher) command(runID, after string) string {
	words := []string{
		shellescape.Quote(a.client.AgentPath()),
		cmdInternal, "agent", cmdWorkspace, "logs", "--source", sourceSetup,
		"--context", shellescape.Quote(a.client.Context()),
		"--id", shellescape.Quote(a.client.Workspace()),
		"--uid", shellescape.Quote(a.uid),
		"--run", shellescape.Quote(runID),
	}
	if after != "" {
		words = append(words, "--after", shellescape.Quote(after))
	}

	return strings.Join(words, " ")
}

// classifyRemoteFailure maps remote stderr to an availability reason. An agent
// that predates the reader rejects the flag or command, which is unsupported
// rather than a failure.
func classifyRemoteFailure(stderr string) string {
	if reason, ok := workspacelogs.ParseUnavailable(stderr); ok {
		return reason
	}
	if strings.Contains(stderr, "unknown flag") || strings.Contains(stderr, "unknown command") {
		return workspacelogs.ReasonRefreshUnsupported
	}

	return workspacelogs.ReasonRefreshFailed
}

func writeJSONLine(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}

// plainWriter prints a transcript. Output text is literal; each line carries
// its stream label so stdout and stderr stay distinguishable. Capture notes go
// to stderr with the availability notices.
type plainWriter struct {
	out     io.Writer
	errOut  io.Writer
	key     string
	midLine bool
}

func (w *plainWriter) page(resp *workspacelogs.ReadResponse) {
	for i := range resp.Records {
		w.record(&resp.Records[i])
	}
}

func (w *plainWriter) record(rec *workspacelogs.Record) {
	switch rec.Kind {
	case workspacelogs.KindOutput:
		w.output(rec)
	case workspacelogs.KindInvocation:
		w.endLine()
		_, _ = fmt.Fprintln(w.out, invocationLine(rec))
	case workspacelogs.KindCapture:
		_, _ = fmt.Fprintf(w.errOut, "capture: %s %s\n", rec.Condition, rec.Detail)
	case workspacelogs.KindProducer:
	}
}

func (w *plainWriter) output(rec *workspacelogs.Record) {
	key := rec.InvocationID + "/" + string(rec.Stream)
	if w.midLine && key != w.key {
		w.endLine()
	}
	w.key = key
	text := rec.Text
	if rec.Elided {
		w.endLine()
		text = fmt.Sprintf("[%d bytes not retained]\n", rec.Bytes)
	}
	label := string(rec.Stream) + " | "
	for text != "" {
		if !w.midLine {
			_, _ = io.WriteString(w.out, label)
			w.midLine = true
		}
		line, rest, hasNewline := strings.Cut(text, "\n")
		if !hasNewline {
			_, _ = io.WriteString(w.out, line)

			return
		}
		_, _ = io.WriteString(w.out, line+"\n")
		w.midLine, text = false, rest
	}
}

func (w *plainWriter) endLine() {
	if w.midLine {
		_, _ = io.WriteString(w.out, "\n")
		w.midLine = false
	}
}

// finish writes the notices that describe how much history is available.
func (w *plainWriter) finish(resp *workspacelogs.ReadResponse, reason string) {
	w.endLine()
	for _, a := range resp.Availability {
		if !a.Available {
			_, _ = fmt.Fprintf(w.errOut, "unavailable: %s %s\n", a.ProducerID, a.Reason)
		}
	}
	if reason != "" {
		_, _ = fmt.Fprintf(w.errOut, "unavailable: %s\n", reason)
	}
	if !resp.Complete {
		_, _ = fmt.Fprintf(w.errOut, "history is incomplete (capture %s)\n", resp.Run.CaptureState)
	}
}

func invocationLine(rec *workspacelogs.Record) string {
	name := rec.Hook
	if name == "" {
		name = rec.Phase
	}
	line := "# " + name
	if rec.HookKey != "" {
		line += " (" + rec.HookKey + ")"
	}
	line += ": " + string(rec.State)
	if rec.Reason != "" {
		line += " " + rec.Reason
	}
	if rec.ExitCode != nil {
		line += fmt.Sprintf(" exit=%d", *rec.ExitCode)
	}
	if rec.Signal != "" {
		line += " signal=" + rec.Signal
	}

	return line
}
