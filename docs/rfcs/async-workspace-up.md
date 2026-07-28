# RFC: Asynchronous `workspace up` and structured status

## Status

Draft. No backward compatibility is preserved by design — breaking changes to
the CLI JSON output contract, the `Runner` interface, the tunnel proto, and
the desktop IPC contract are in scope.

## Problem

`workspace up` is a single synchronous call chain from CLI flag parsing all
the way to IDE launch. Every layer blocks on the one below it and returns a
single terminal value:

```
cmd/workspace/up.Run
  -> devsyUp -> dispatchClient (blocks on client.Up / tunnel RunUpServer)
    -> devcontainer.Runner.Up(ctx, options, timeout) (*config.Result, error)
      -> runSingleContainer / runDockerCompose
        -> build (docker/buildkit exec, blocking)
        -> driver.Run (blocking)
        -> setupContainer -> inner tunnel -> RunPreAttachHooks (blocking up to waitFor)
  -> finalizeUp (SSH config, tunnel, IDE open)
```

The only two structured signals that cross a process boundary are:

- `tunnel.LogMessage` — a level (`DEBUG/INFO/DONE/WARNING/ERROR`) + freeform
  string, sent continuously.
- `tunnel.SendResult` — the final `config.Result`, sent exactly once, at the
  end.

There is no phase, step, or percentage concept anywhere. The desktop app
compensates by spawning the CLI as a subprocess, parsing every stdout line as
a zap JSON log record, and detecting completion by
`line.includes('"outcome":"success"')` (`desktop/src/main/ipc.ts:755`) — a
string sniff on log text, not a contract.

Consequences:

- The CLI cannot report progress faster than log lines happen to convey it.
- The desktop app cannot show "building image" vs "running postCreate" vs
  "waiting for port" as distinct states — only "still running" or "done".
- Nothing downstream of `up` (IDE open, SSH tunnel) can start until the
  entire blocking chain returns, even when only the pieces it actually
  depends on (e.g. container is running, `waitFor` phase reached) are ready.

## Goals

1. Every meaningful step of `up` (resolve config, initializeCommand, build
   image, start container, inject agent, run each lifecycle hook, waitFor,
   ready) emits a structured, typed status event as it starts/finishes —
   not just a log line.
2. The event stream crosses every process boundary that today only carries
   `LogMessage`/`Result` (the outer CLI<->agent tunnel and the inner
   container-setup tunnel).
3. The CLI's `--result-format json` mode emits status as NDJSON as it
   happens, not only a single envelope at the end.
4. The desktop app consumes the structured stream directly — no string
   sniffing on log content for control flow.
5. Steps that don't depend on each other can run concurrently where the
   underlying operation allows it (e.g. pulling/building images for
   docker-compose services, or preparing SSH config while lifecycle hooks
   past `waitFor` are still running in the background).
6. `up` can return control to its caller as soon as the work is submitted,
   without waiting for the workspace to be ready — the caller (CLI user or
   desktop UI) decides whether to wait or poll. This is the actual meaning of
   "asynchronous" this RFC targets: not reordering the devcontainer-spec's
   inherently sequential build/inject/hook chain (that chain has real data
   dependencies and reordering it would be incorrect), but decoupling
   *submission* from *completion* the way the platform/daemon path already
   decouples them for remote workspaces.

## Non-goals

- Changing the devcontainer.json spec or lifecycle hook semantics themselves.
- A general pub/sub system for arbitrary future features — scope is the
  `up`/`build`/`setup` path.
- Preserving the current single-shot JSON result envelope shape unchanged;
  it is superseded by the event stream plus a final terminal event.

## Design

### 1. Status/phase model (`pkg/devcontainer/status`)

A closed set of phases mirroring the real dependency chain of `up`:

```go
type Phase string

const (
    PhaseCloningRepository    Phase = "cloning_repository"
    PhaseResolvingConfig      Phase = "resolving_config"
    PhaseInitializeCommand    Phase = "initialize_command"
    PhaseBuildingImage        Phase = "building_image"
    PhaseStartingContainer    Phase = "starting_container"
    PhaseInjectingAgent       Phase = "injecting_agent"
    PhaseRunningLifecycleHook Phase = "running_lifecycle_hook" // + HookName field
    PhaseWaitingFor           Phase = "waiting_for"
    PhaseReady                Phase = "ready"
    PhaseFailed               Phase = "failed"
)

type Event struct {
    Phase   Phase
    Step    string // e.g. hook name, service name; empty when not applicable
    Started bool   // true = entering phase, false = phase complete
    Err     string // set only when Phase == PhaseFailed
}

type Reporter interface {
    Report(Event)
}
```

A `NopReporter` and a `ChannelReporter` (buffered channel + drain goroutine,
same shape as the existing `tunnelLogger` pattern) are the two initial
implementations.

### 2. `Runner` interface takes a `Reporter`

```go
type Runner interface {
    Up(ctx context.Context, options UpOptions, timeout time.Duration, r status.Reporter) (*config.Result, error)
    // ... unchanged otherwise
}
```

Breaking change: every existing caller of `Up` must pass a reporter
(`status.Nop()` is fine where nobody listens yet). `run.go`, `single.go`,
`compose_up.go`, `build.go`, `setup.go`, `lifecyclehooks.go` each call
`r.Report(...)` at the start/end of their step instead of (or in addition to)
`log.Info`.

### 3. Tunnel proto: `StatusUpdate` RPC

```protobuf
message StatusUpdate {
  string phase = 1;
  string step = 2;
  bool started = 3;
  string error = 4;
}

service Tunnel {
  ...
  rpc Status(StatusUpdate) returns (Empty) {}
}
```

Both tunnels that exist today (`RunUpServer` for the outer CLI<->agent
session, `RunSetupServer` for the inner container-setup session) forward
`status.Reporter` events over this RPC the same way `tunnelLogger` forwards
`LogMessage` today (buffered chan + worker goroutine, `pkg/agent/tunnelserver/logger.go`
is the template). The host-side tunnel client fans inbound `StatusUpdate`
messages into a `status.Reporter` the CLI process owns.

### 4. CLI: NDJSON status stream

`cmd/workspace/up` gets a `status.Reporter` that, in JSON result-format mode,
writes one compact JSON object per event to stdout immediately (not
buffered until exit), followed by the existing terminal `ResultEnvelope`/
`ErrorEnvelope` as the final line. Plain-text mode renders the same events
as human-readable progress lines (this can replace a good number of today's
ad hoc `log.Info` progress messages in the up path).

This is a breaking change to the CLI's JSON output contract: consumers that
assumed exactly one JSON object on stdout must now expect a stream and
identify the terminal one (e.g. by a `"kind":"result"` discriminator field
added to `ResultEnvelope`/`ErrorEnvelope`, vs `"kind":"status"` on events).

### 5. Desktop: structured consumption

`desktop/src/main/ipc.ts`'s `workspace_up` handler stops treating stdout as
opaque log text for control flow. It parses each NDJSON line, and:

- `"kind":"status"` lines update a per-workspace phase state machine and are
  forwarded to the renderer as a new `workspace-status` IPC event (structured
  `{ commandId, phase, step, started }`), replacing the current
  `command-progress` line-batching for control-flow purposes (raw log text
  can still be forwarded for the log viewer, but is no longer load-bearing).
- `"kind":"result"` lines replace the `line.includes('"outcome":"success"')`
  sniff.

`desktop/src/shared/cli-error.ts` (or a new `cli-status.ts`) gains the shared
TypeScript types for `StatusEvent`/`ResultEnvelope` mirroring the Go JSON
shapes, generated or hand-kept in sync (no codegen exists today; hand-kept
with a comment pointing at the Go source of truth is acceptable for now).

### 6. Two execution flows, and standard verbs for the durable one

This is the piece that actually decouples "start provisioning" from "wait
for it to finish," per Goal 6.

**Synchronous** (default, unchanged): `workspace up <source>` attaches and
waits, exactly as before — no reason to force a UX change on the common
interactive case.

**Durable**: `workspace up <source> --detach`/`-d` submits the same pipeline
as a background task and returns immediately, printing
`{"kind":"task","id":"..."}`. "Durable" here means the task's state
survives past the submitting process's exit — it's a file, not something
held in memory by a process someone has to keep watching.

**`pkg/task`** is that file's home: one JSON file per task under
`PathManager.TaskDir()` (`<state dir>/tasks/<id>.json`), written atomically
(temp file + rename). `State` carries `Status` (`pending` / `running` /
`succeeded` / `failed`), the most recent `Phase`/`Step`, labels
(`Command`, `WorkspaceID`) for listing, a `PID` for cancellation, and — once
terminal — the final `Error` or `*config.Result`. `Task.Reporter()` returns
a `status.Reporter` that persists each event into the file.

**`cmd/workspace/up/detach.go`** implements submission: create a task, then
re-exec the current binary with this invocation's original arguments —
`--detach` swapped for the hidden `--task-id <id>` — as a background process
(`pkg/command.StartBackground`, the same primitive already used for deferred
lifecycle hooks). The parent does no devcontainer work itself. **The
re-exec'd child** runs through the exact same `execute` → `Run` →
`executeDevsyUp` → `finalizeUp` path an attached `up` always has — nothing
about the devcontainer-spec-ordered pipeline changes; `Run` just tees its
`status.Reporter` (`status.Tee`) into both the normal NDJSON/log output *and*
the task file, and calls `Succeed`/`Fail` on the task at the same points it
would otherwise just return. The child *is* today's `up`, observed from two
places instead of one — which is why devcontainer-spec ordering needed no
verification beyond "did we change anything in `pkg/devcontainer`?" (no).

**The task resource gets standard verbs** (`cmd/workspace/task.go`), rather
than one command overloaded with `--wait`/`--cancel` flags, so it reads like
every other resource-oriented CLI (kubectl, docker, gh):

| Command | Aliases | Verb category |
|---|---|---|
| `workspace task list` | `ls` | List |
| `workspace task get <id>` | `describe`, `show` | Get/Show/Describe |
| `workspace task logs <id> [-f\|--follow]` | `attach` | Logs/Tail (docker/kubectl `-f`) |
| `workspace task cancel <id>` | `stop` | Stop/Cancel |
| `workspace task rm <id> [--force]` | `delete`, `remove` | Delete/Rm/Remove |

`get`/`logs` without `-f` are the same operation (one snapshot); `logs -f`
polls on an interval (`--interval`, default 500ms), printing the phase
observed at each poll when it differs from the last, then the final
result/error envelope (exit code reflects success/failure). `State` stores
only the current phase/step, not a history — this is a latest-state
snapshot, not a replay log, so a burst of transitions between two polls can
be collapsed to just the last one observed. `cancel` sends the signal
recorded via `Task.SetPID` (set by the detached child on itself) and marks
the task failed with `task.ErrCanceled`; canceling successfully is reported
as success even though the task's own terminal state is "failed." `rm`
refuses to delete a non-terminal task unless `--force`, which cancels it
first (stopping the worker) before deleting — mirroring `kubectl delete
--force`/`docker rm -f`, both of which also stop the resource, not just
remove its record.

This gives every caller — interactive CLI, script, or desktop — the choice
DevContainer-remote already had for platform workspaces: submit and don't
wait, or submit and wait, with status observable in both cases, using verbs
that don't need this doc to explain them.

**Desktop** (`desktop/src/main/ipc.ts`): `workspace_up` now submits via
`up --detach` (`cli.run`, one-shot), then streams `workspace task logs <id>
--follow` instead of streaming `up` directly — the NDJSON envelope parsing
is unchanged, since `logs` emits the same envelope shapes `up` does. A new
`activeUpTasks: Map<workspaceId, taskId>` tracks the task backing the real
provisioning process; `quiesceWorkspace` (run before stop/delete) now issues
`workspace task cancel <id>` against it before touching any child process,
because the child it *can* see (the `logs --follow` poller) is not the
process doing the work — killing only the poller would silently leave
provisioning running. This also means the desktop app can now query a
task's status even if Electron itself was restarted while `up` was running,
which it could not do before (progress was tied to the lifetime of the
child process it spawned).

### Concurrency

Structured status is a prerequisite for observing overlap, not a substitute
for it — the point of this RFC is that steps of `up` which don't depend on
each other actually run at the same time. Implemented so far:

- **Feature resolution** (`pkg/devcontainer/feature.getUserFeatures`): each
  user-configured feature is fetched (OCI pull, tarball download, or local
  read) concurrently via `errgroup`, instead of one at a time. `lockfileState`
  gained a mutex around its `entries` map since `record` is now called from
  multiple goroutines. Race-tested with `go test -race`.
- **Agent binary prefetch** (`runner.prefetchAgentBinary`, called from
  `runSingleContainer`): the agent binary download/cache-read starts on a
  background goroutine at the same time as container resolution
  (`resolveContainer`, which builds and starts the container), instead of
  starting only once the container is already running and injection is
  ready. It's best-effort and silently discards its result on failure — the
  real acquisition on the injection path re-runs from scratch and simply
  hits the now-warm on-disk cache in the common case.
- **`daemonclient` (platform/pro) `Up` path**
  (`pkg/client/clientimplementation/daemonclient/up.go`): `UpOptions` gained
  a `Reporter status.Reporter` field, threaded through
  `waitTaskDone`/`observeTask`/`printLogs`. The remote task the daemon
  observes runs the same `devsy` CLI, so its forwarded log stream already
  contains the same `kind":"status"` NDJSON lines a local `up` emits; a new
  `statusSniffingWriter` splits that stream into lines, forwards recognized
  status lines to the `Reporter` (via the new `config.ParseStatusLine`
  helper), and passes every other line through to the existing zap-log
  renderer unchanged. This does not make the call return before the remote
  task finishes — that's a correctness requirement, not a gap: `Up` must
  return the final `*config.Result`, which only exists once the remote task
  completes — but it does mean this path now participates in the same
  structured-status model as the local path instead of being informationally
  disconnected from it (previously: opaque zap JSON only, `status.Reporter`
  events did not exist for this path at all). Covered by
  `statusSniffingWriter`'s dedicated unit tests (race-tested).

Already-async before this RFC, noted for completeness: `DeferredHooks` (the
lifecycle hooks that run after `waitFor`) are launched as a detached
background OS process (`cmd/internal/agentcontainer/setup.go:startDeferredHooks`)
rather than awaited inline — `up` does not block on them today. Likewise,
docker-compose service builds are not a sequential loop in this codebase to
begin with: `runComposeBuild` shells out once to `docker compose ... build`
(`compose_build.go:runComposeBuild`), and the Compose CLI itself parallelizes
independent service builds — there's no Go-level per-service loop here to
convert.

Remaining candidate, not yet converted:

- Per-lifecycle-hook granularity: the inner container-setup tunnel currently
  reports one `PhaseRunningLifecycleHook` span for the whole pre-`waitFor`
  hook sequence rather than one per hook.

This RFC does not mandate converting every step in one pass — each
conversion above was verified independently (build + `go test -race`) rather
than landed as one big-bang rewrite.

## Breaking changes summary

| Area | Before | After |
|---|---|---|
| `devcontainer.Runner.Up` | `(ctx, options, timeout) (*Result, error)` | `(ctx, options, timeout, status.Reporter) (*Result, error)` |
| Tunnel proto | `Log` + `SendResult` only | adds `Status` RPC |
| CLI JSON stdout | one `ResultEnvelope`/`ErrorEnvelope` object | NDJSON stream of status events + one terminal result/error object, each tagged with a `kind` discriminator |
| Desktop completion detection | `stdout.includes('"outcome":"success"')` | parse `"kind":"result"` NDJSON line |
| Desktop progress IPC | `command-progress` (raw log line batches) | new `workspace-status` (structured phase) + `command-progress` (raw log, display-only) |
| `client.UpOptions` | no status visibility | gains `Reporter status.Reporter` |
| `workspace up` | always attaches and waits | `--detach`/`-d` submits a `pkg/task` and returns immediately; default behavior unchanged |
| Local task observability | none — progress only exists on the invoking process's stdout | `workspace task {list,get,logs}` reads `pkg/task`'s on-disk state, independent of any live process |
| Canceling an in-progress `up` | kill the CLI process desktop happened to spawn | `workspace task cancel <id>` (PID-tracked, works regardless of who's watching) |
| Desktop `workspace_up` IPC | spawns `up` directly, streams its output | spawns `up --detach` (one-shot), then streams `workspace task logs <id> --follow`; `quiesceWorkspace` cancels the task, not just its poller |
| `client.Status` | `Running`/`Busy`/`Stopped`/`NotFound` | adds `Provisioning` and `Failed`: `workspaceClient.Status()` looks at the most recently started `up` task on record for the workspace and reports one of them instead of `NotFound` when it's more informative — `Provisioning` while in flight, `Failed` if it errored — so `workspace status` doesn't claim a workspace was never started, or silently forget a failed attempt |
| `git clone` progress | raw `--progress` output logged as-is (unreadable: `\r`-delimited updates collapse into one giant line once captured as log lines) | new `status.PhaseCloningRepository`: `pkg/git`'s `progressWriter` parses the `\r`-delimited updates and reports them as `status.Event`s (thinned to every 10%) instead of logging them; plain, JSON, and desktop consumption all get clone progress through the same pipeline as every other phase, for free |
| `log.Writer` (subprocess Stdout/Stderr, ~65 call sites) | raw byte passthrough straight to the stderr sink, bypassing the configured encoder — every JSON-mode log call except this one produced `{"level":...}`, this one leaked unencoded text into the stream | line-buffers and calls `Info`/`Debug`/`Error` per complete line, so subprocess output goes through the same json/text/logfmt encoder as everything else; contradicted its own doc comment ("writes each line as a log entry") before this fix |

## Rollout

1. Land `pkg/devcontainer/status` (event model + Nop/Channel reporters) with
   no behavior change — nothing calls `Report` with anything meaningful yet.
2. Thread `status.Reporter` through `Runner.Up` and its call chain, emitting
   real events at each existing step boundary (no new proto yet — reporter is
   in-process only for the direct/non-tunneled drivers first).
3. Add the `StatusUpdate` tunnel RPC and wire both tunnel sessions to forward
   events across the process boundary.
4. Switch CLI JSON output to NDJSON + discriminator, update `pkg/output`.
5. Update desktop `ipc.ts`/`cli.ts`/shared types to consume structured events;
   remove the string-sniff.
6. Opportunistically parallelize the concurrency candidates listed above,
   one at a time, each covered by its own e2e test under `e2e/tests/up*`.
7. Land `pkg/task` and `workspace up --detach`, reusing the reporter
   plumbing from steps 1-4 unchanged — the detached child is the same
   attached-mode code path, just with its reporter teed into a task file.
   No changes to `pkg/devcontainer` were needed for this step, which is
   itself evidence the devcontainer-spec ordering wasn't touched.
8. Wire the desktop app onto the submit/attach model:
   `desktop/src/main/ipc.ts`'s `workspace_up` handler now submits via
   `up --detach` (`cli.run`, one-shot — returns as soon as the task exists),
   then streams the task's status instead of streaming `up` directly. A new
   `activeUpTasks: Map<workspaceId, taskId>` tracks the task backing the real
   (detached) provisioning process; `quiesceWorkspace` (called before
   stop/delete) now cancels it before touching any live child process,
   because the child it *can* see (the poller) is no longer the process
   actually doing the work — killing only the poller would silently leave
   provisioning running. This needed `pkg/task` to gain PID tracking
   (`Task.SetPID`, set by the detached child on itself as soon as it opens
   its task) and `Task.Cancel` (`pkg/command.Kill` on that PID, then
   `Fail(ErrCanceled)`) — the "some other commands will be impacted" part
   of the original ask materializing.
9. Replace the ad hoc `workspace task <id> [--wait] [--cancel]` command with
   standard verbs on the task resource — `list`/`get`/`logs [-f]`/`cancel`/
   `rm [--force]` — matching kubectl/docker/gh conventions instead of one
   command overloaded with flags. `pkg/task` gained `CreateOptions`
   (`Command`/`WorkspaceID` labels, so `list` is useful) and `Store.Delete`
   for `rm`. Desktop's two call sites (poll, cancel) were updated to the new
   verbs in the same change — see the table above.
10. Close the loop between `pkg/task` and container-level status:
    `workspaceClient.Status()` (`pkg/client/clientimplementation`) now
    reports `client.StatusProvisioning` or `client.StatusFailed` in place of
    `StatusNotFound` based on the most recently started `up` task on record
    for the workspace (`workspaceClient.taskStatusOverride()` lists
    `pkg/task`, matches on `WorkspaceID`+`Command`, and picks the task with
    the latest `StartedAt`) — `Provisioning` while it's non-terminal,
    `Failed` if it ended in error, and no override at all if it succeeded
    (a successful task implies nothing; if the container's still missing
    after that, something else removed it, and `NotFound` is the honest
    answer). The detached child corrects its task's `WorkspaceID` label to
    the client's resolved ID once known (`Task.SetWorkspaceID`, called from
    `Run`) rather than trusting the submitting process's guess (raw
    `--id`/source string), since the lookup depends on that label being
    accurate.
11. Route git clone progress through the same structured pipeline instead of
    dropping it. First pass tried reformatting git's raw `--progress` text
    for readability (a client-side writer splitting on `\r`); simpler and
    more correct was noticing that no code anywhere parses that text for a
    UI, so there was nothing to preserve about the raw format at all — the
    real fix was giving clone progress the same treatment every other phase
    already gets. `status` gains `PhaseCloningRepository`; `pkg/git` gains
    `WithProgressReporter` (a `RepoOption`) and a `progressWriter` that
    parses `\r`-delimited "label: NN% (x/y)" updates and reports them as
    `status.Event`s (thinned to every 10%, exact-duplicate frames dropped)
    instead of writing them to the log — informational lines like "Cloning
    into '...'" still log normally. The reporter is threaded from the
    agent's tunnel client (`tunnelserver.NewTunnelStatusReporter`, already
    built in step 3) through `prepareWorkspace`/`prepareGitWorkspace` into
    `agent.CloneWorkspaceParams`, so it crosses the same tunnel boundary as
    every other phase and reaches the CLI's NDJSON/plain output and
    desktop's `workspace-status` IPC event for free — no new plumbing
    needed on either of those ends.

Each phase should be independently mergeable and leaves the system in a
working (if not yet fully async) state — the plan is incremental even though
no compatibility shims are kept between phases.

12. Harden `pkg/task` against the two ways it's actually multi-writer: a
    detached worker reporting progress and a separate `task cancel`/`rm
    --force` process both mutate the same state file, and `Get`/`Delete`
    take a caller-supplied ID from argv. `Store.update` now holds an OS file
    lock (`github.com/gofrs/flock`, one `.lock` file per task, 5s timeout)
    around each read-modify-write, replacing a process-local `sync.Mutex`
    that only ever protected against races within one process. `Cancel`
    reads the terminal check and applies the state transition inside that
    same locked update instead of as a separate Get-then-Fail, so a
    concurrent worker report can't land between them. `Store.path` rejects
    any ID that isn't a single clean path component, closing a path
    traversal a raw `../../etc/passwd`-style ID could otherwise reach.
    `pkg/command.kill` (Unix) also stopped discarding `syscall.Kill`'s
    error — it now returns real failures and only treats "process already
    gone" (`ESRCH`) as success, so `Cancel` can't silently claim a kill
    succeeded when it didn't.
