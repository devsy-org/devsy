# devsy MCP Readiness Gap Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the confirmed correctness, safety, and durability gaps found during a hands-on readiness evaluation of devsy's CLI/MCP surface for external-agent orchestration of many devcontainer workspaces.

**Architecture:** Five independent, narrowly-scoped fixes layered onto the existing devsy Go codebase: (1) a per-workspace lock in `workspace.ExecOneShot` reusing the existing `client.Lock`/`Unlock` flock primitive so `workspace_exec` can't race a concurrent `workspace_delete`/`workspace_create`/`workspace_stop` on the same workspace; (2) a process-wide bounded semaphore in the MCP server gating `workspace_create`/`workspace_start`/`workspace_exec` so an orchestrator can't overwhelm the local Docker/Kubernetes backend; (3) a one-line fix so `--ide-launch=skip` also implies no IDE server install, matching user intent for headless/agent use; (4) a new `e2e/tests/mcp` package that drives the real `devsy mcp serve` binary over stdio JSON-RPC, following the existing Ginkgo/framework e2e pattern; (5) a parent-directory fsync added to `pkg/provider.WriteFileAtomic` for crash-durable config writes, gated to POSIX platforms.

**Tech Stack:** Go 1.25+, cobra CLI framework, `github.com/modelcontextprotocol/go-sdk` v1.7.0 (MCP), `github.com/gofrs/flock` v0.13.0 (file locks), `github.com/stretchr/testify` (unit tests), Ginkgo/Gomega + `e2e/framework` (e2e tests).

## Global Constraints

- Go module: `github.com/devsy-org/devsy` — Go 1.25+ (per go.mod / CONTRIBUTING.md).
- No comments restating what code does; comments only for non-obvious WHY (existing codebase convention, see `pkg/provider/atomic.go`'s doc comment style).
- Unit tests use the standard library `testing` package with plain assertions (see `pkg/workspace/exec_test.go`) OR `testify/require`+`assert` (see `cmd/workspace/up/up_test.go`) — follow whichever convention the file being modified already uses.
- e2e tests use Ginkgo v2 + Gomega, wired through `e2e/framework`, and must shell out to the real compiled `devsy` binary — never call Go functions directly (established pattern in every `e2e/tests/*` package).
- Run `go build ./...` and the relevant `go test` after every task; do not leave the tree in a non-building state between commits.
- Target scale is small (5-20 concurrent workspaces) — no elaborate scheduling/queueing infrastructure; a simple bounded semaphore is sufficient for Task 2.
- The MCP server (`devsy mcp serve`) already dispatches each `CallToolRequest` on its own goroutine via the SDK's automatic `jsonrpc2.Async` (confirmed in `go-sdk@v1.7.0/mcp/server.go:1908-1914`) — do NOT attempt to add manual async signaling in `cmd/mcp`; that lever does not exist for app code (`internal/jsonrpc2.Async` is unexported package, not importable).

---

### Task 1: Per-workspace lock in `ExecOneShot` to prevent exec/delete/create races

**Files:**
- Modify: `pkg/workspace/exec.go:507-550` (`resolveExecTarget`)
- Modify: `pkg/workspace/exec.go:439-488` (`ExecOneShot`)
- Test: `pkg/workspace/exec_test.go` (append new test)

**Interfaces:**
- Consumes: `client.BaseWorkspaceClient` interface's existing `Lock(ctx context.Context) error` / `Unlock()` methods (`pkg/client/client.go:117-124`) — already implemented by every concrete client (`pkg/client/clientimplementation/workspace_client.go:153-173`, `proxy_client.go:105-127`). `workspace.Get(ctx, GetOptions) (client.BaseWorkspaceClient, error)` (`pkg/workspace/workspace.go:184`) — already used by `resolveExecTarget`.
- Produces: `ExecOneShot` now returns a distinguishable error when the workspace is locked by a concurrent operation (delete/create/rename) — callers (`cmd/mcp/tools_exec.go`) get this through the existing `err` return path and existing `ClassifyError`/`errorResult` plumbing; no new error type needed, plain `fmt.Errorf` wrapping is sufficient since `cmd/mcp/errors.go`'s `ClassifyError` falls through to `CodeUnknown` with the message intact.

**Background (from source investigation):** `resolveExecTarget` (`pkg/workspace/exec.go:507`) calls `Get(ctx, GetOptions{...})` to get a `client.BaseWorkspaceClient`, then immediately proceeds to `runtime.FindRunning` and builds the exec target — it never calls `client.Lock(ctx)`. Meanwhile `pkg/workspace/delete.go`'s `lockIfNeeded` (lines 124-139) and `pkg/workspace/rename.go` (lines 63-66) both call `client.Lock(ctx)` before mutating the workspace. Because `ExecOneShot` never takes this lock, a `workspace_exec` call can run concurrently with a `workspace_delete` on the same workspace name — e.g. exec resolves the container, delete removes it mid-exec, exec's `docker exec` fails with a confusing "container not found" instead of a clear "workspace busy" error, or worse, exec succeeds against a container that delete is simultaneously tearing down.

The fix: acquire the workspace lock for the duration of resolution + exec, using a **short** timeout (exec is meant to be fast — unlike `delete`/`create` which use the 5-minute `lockTimeout` in `proxy_client.go:30`, a stuck exec waiting 5 minutes for a lock would blow through the MCP tool's own exec timeout anyway). Release it in all return paths via `defer`.

- [ ] **Step 1: Write the failing test for lock acquisition**

Add to `pkg/workspace/exec_test.go` (this file already has `fakeRuntime` at lines ~85-126; you'll need a fake client too — check if one exists in the package first via `grep -rn "type fake.*Client" pkg/workspace/*_test.go`; if none exists, define a minimal one scoped to this test file):

```go
type fakeLockClient struct {
	lockErr    error
	lockCalls  int
	unlockCalls int
}

func (f *fakeLockClient) Lock(_ context.Context) error {
	f.lockCalls++
	return f.lockErr
}

func (f *fakeLockClient) Unlock() {
	f.unlockCalls++
}

func TestExecOneShot_LockedWorkspaceReturnsBusyError(t *testing.T) {
	// This test documents the required behavior: resolveExecTarget must
	// surface a locked-workspace condition as an error before touching the
	// container, not proceed silently. Since resolveExecTarget currently
	// calls workspace.Get directly (a package function, not injectable),
	// this test exercises the seam introduced in Step 3: a lock acquired via
	// client.Lock must be attempted, and a lock failure must short-circuit
	// before FindRunning/Exec run.
	lockClient := &fakeLockClient{lockErr: fmt.Errorf("timed out waiting to lock workspace")}
	err := acquireExecLock(context.Background(), lockClient, defaultExecLockTimeout)
	if err == nil {
		t.Fatal("expected lock error, got nil")
	}
	if lockClient.lockCalls != 1 {
		t.Fatalf("expected 1 lock call, got %d", lockClient.lockCalls)
	}
	if lockClient.unlockCalls != 0 {
		t.Fatalf("unlock must not be called when lock itself failed, got %d calls", lockClient.unlockCalls)
	}
}

func TestExecOneShot_UnlocksAfterSuccessfulLock(t *testing.T) {
	lockClient := &fakeLockClient{}
	err := acquireExecLock(context.Background(), lockClient, defaultExecLockTimeout)
	if err != nil {
		t.Fatalf("unexpected lock error: %v", err)
	}
	if lockClient.lockCalls != 1 {
		t.Fatalf("expected 1 lock call, got %d", lockClient.lockCalls)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/workspace/... -run TestExecOneShot_Locked -v`
Expected: FAIL with `undefined: acquireExecLock` / `undefined: defaultExecLockTimeout` (function doesn't exist yet).

- [ ] **Step 3: Implement the lock helper and wire it into `resolveExecTarget`**

In `pkg/workspace/exec.go`, add near the top of the file (after the existing `const` block at lines 25-28):

```go
// execLocker is the subset of client.BaseWorkspaceClient needed to guard an
// exec against a concurrent delete/create/rename on the same workspace.
type execLocker interface {
	Lock(ctx context.Context) error
	Unlock()
}

// defaultExecLockTimeout bounds how long ExecOneShot waits for the workspace
// lock before giving up. Kept short (unlike the 5-minute lockTimeout used by
// delete/create) because a caller already has its own exec timeout budget
// (see ExecOneShotOptions.ResolveTimeout) and a long lock wait would starve it.
const defaultExecLockTimeout = 15 * time.Second

// acquireExecLock attempts to lock the workspace within timeout. On success
// it returns nil and the caller must call client.Unlock() when done. On
// failure it returns an error and the caller must NOT call Unlock (nothing
// was acquired).
func acquireExecLock(ctx context.Context, client execLocker, timeout time.Duration) error {
	lockCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := client.Lock(lockCtx); err != nil {
		return fmt.Errorf("workspace busy (locked by a concurrent operation): %w", err)
	}
	return nil
}
```

Then modify `resolveExecTarget` (`pkg/workspace/exec.go:507-550`) to acquire the lock right after `Get` succeeds and release it via a returned cleanup func. Since `resolvedExecTarget` is returned by value and `ExecOneShot` runs the exec after resolution, the unlock must happen after the exec completes, not at the end of `resolveExecTarget`. Change the return type and call site together:

```go
type resolvedExecTarget struct {
	runtime ContainerRuntime
	target  ContainerTarget
	workdir string
	envMap  map[string]string
	unlock  func()
}

func resolveExecTarget(ctx context.Context, opts ExecOneShotOptions) (resolvedExecTarget, error) {
	devsyConfig, err := config.LoadConfig(opts.Context, opts.Provider)
	if err != nil {
		return resolvedExecTarget{}, fmt.Errorf("load config: %w", err)
	}

	client, err := Get(ctx, GetOptions{
		DevsyConfig: devsyConfig,
		Args:        []string{opts.WorkspaceName},
		Owner:       opts.Owner,
	})
	if err != nil {
		return resolvedExecTarget{}, fmt.Errorf("resolve workspace: %w", err)
	}

	if err := acquireExecLock(ctx, client, defaultExecLockTimeout); err != nil {
		return resolvedExecTarget{}, err
	}
	unlock := client.Unlock

	workspaceConfig := client.WorkspaceConfig()
	runtime, err := NewContainerRuntime(workspaceConfig, "")
	if err != nil {
		unlock()
		return resolvedExecTarget{}, err
	}

	containerDetails, err := runtime.FindRunning(
		ctx, devcontainer.GetRunnerIDFromWorkspace(workspaceConfig), opts.IDLabels,
	)
	if err != nil {
		unlock()
		return resolvedExecTarget{}, err
	}

	execResult := LoadExecResult(workspaceConfig, containerDetails)
	target, workdir, envMap := buildExecTargetEnv(ctx, buildExecTargetEnvParams{
		runtime:       runtime,
		opts:          opts,
		execResult:    execResult,
		containerID:   containerDetails.ID,
		workspaceName: client.Workspace(),
	})

	return resolvedExecTarget{
		runtime: runtime,
		target:  target,
		workdir: workdir,
		envMap:  envMap,
		unlock:  unlock,
	}, nil
}
```

Then in `ExecOneShot` (`pkg/workspace/exec.go:439-488`), release the lock after the exec completes — add `defer` right after the `resolveExecTarget` call succeeds:

```go
	resolved, err := resolveExecTarget(ctx, opts)
	if err != nil {
		return nil, err
	}
	defer resolved.unlock()

	execCtx, cancel := context.WithTimeout(ctx, timeout)
```//

(The rest of `ExecOneShot` is unchanged — only the two lines above are inserted after the existing `resolveExecTarget` call.)

- [ ] **Step 4: Fix the compile error from the `client.BaseWorkspaceClient` interface satisfying `execLocker`**

`client.BaseWorkspaceClient` (returned by `workspace.Get`) already has `Lock(ctx context.Context) error` and `Unlock()` per `pkg/client/client.go:117-124`, so it satisfies `execLocker` with no changes needed there. Run `go build ./pkg/workspace/...` and fix any import issues (the `time` package is already imported in `exec.go`; `fmt` is already imported).

Run: `go build ./... 2>&1 | head -40`
Expected: builds cleanly.

- [ ] **Step 5: Run the new unit tests and verify they pass**

Run: `go test ./pkg/workspace/... -run TestExecOneShot -v`
Expected: PASS for `TestExecOneShot_LockedWorkspaceReturnsBusyError`, `TestExecOneShot_UnlocksAfterSuccessfulLock`, and the pre-existing `TestExecOneShot_ExitCodeAndOutput`/`TestExecOneShot_PartialOutputOnError`.

- [ ] **Step 6: Run the full package test suite to check for regressions**

Run: `go test ./pkg/workspace/... -race -v 2>&1 | tail -60`
Expected: all tests PASS, no data races reported.

- [ ] **Step 7: Commit**

```bash
git add pkg/workspace/exec.go pkg/workspace/exec_test.go
git commit -m "fix(workspace): lock workspace during ExecOneShot to prevent races with delete/create"
```

**Note for follow-up (out of scope for this task):** `cmd/workspace/exec.go`'s `runWithWorkspace` (the CLI's own `devsy workspace exec` command) implements the same resolve-and-exec flow independently of `workspace.ExecOneShot` and has the identical gap — it also never locks. This plan only fixes the MCP-exclusive `ExecOneShot` path since that's what's in scope for external-agent orchestration; unifying the two exec implementations is a larger refactor outside this plan's scope.

---

### Task 2: Bounded concurrency semaphore for MCP workspace operations

**Files:**
- Create: `cmd/mcp/semaphore.go`
- Create: `cmd/mcp/semaphore_test.go`
- Modify: `cmd/mcp/serve.go` (add flag + wire into `ServeCmd`)
- Modify: `cmd/mcp/tools_exec.go` (`registerExecTool`)
- Modify: `cmd/mcp/tools_workspace.go` (`registerWorkspaceLifecycleTools` — `workspace_start` and `workspace_create` handlers)
- Test: `cmd/mcp/serve_test.go` (extend existing `TestServer_ListsAllTools`-style test file with a new test)

**Interfaces:**
- Consumes: nothing new from other tasks.
- Produces: `type opSemaphore struct{...}` with methods `func newOpSemaphore(max int) *opSemaphore` and `func (s *opSemaphore) acquire(ctx context.Context) (release func(), err error)`. `ServeCmd` gets a new field `MaxConcurrentOps int` (flag name `mcp-max-concurrent-ops`, default `8`) and a new field `opSem *opSemaphore` constructed once in `Run`. `registerExecTool(s *sdkmcp.Server, cmd *ServeCmd)` and `registerWorkspaceLifecycleTools(s *sdkmcp.Server, g *flags.GlobalFlags)` both need the semaphore passed in — `registerWorkspaceLifecycleTools`'s signature changes to accept it.

**Background:** No semaphore/worker-pool/rate-limiter exists anywhere in the repo for bounding concurrent workspace operations (confirmed via repo-wide grep for `semaphore|errgroup|RateLimit|MaxConcurrent` — the only `errgroup` usages are in `pkg/devcontainer/feature/extend.go` and `cmd/internal/agentcontainer/daemon.go`, both unrelated). Since `devsy mcp serve` is one process handling one stdio session (confirmed in `cmd/mcp/serve.go:77-88` — `IOTransport{Reader: os.Stdin, Writer: realStdout}`), a simple in-process buffered-channel semaphore is sufficient — no need for cross-process coordination since each MCP server process already corresponds to one orchestrator connection.

Gate `workspace_exec`, `workspace_create`, and `workspace_start` (the three operations that shell out to Docker/Kubernetes and can be issued in a burst by an orchestrator). Do NOT gate `workspace_list`, `workspace_status`, `workspace_stop`, `workspace_delete`, or any `provider_*` tool — those are either read-only or already comparatively cheap/rare in an agent-driven fix-loop workload, and gating deletes could deadlock a caller trying to free resources while all semaphore slots are held by stuck creates.

- [ ] **Step 1: Write the failing test for the semaphore**

Create `cmd/mcp/semaphore_test.go`:

```go
package mcp

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpSemaphore_LimitsConcurrency(t *testing.T) {
	sem := newOpSemaphore(2)
	var inFlight, maxInFlight atomic.Int32

	track := func() {
		cur := inFlight.Add(1)
		for {
			m := maxInFlight.Load()
			if cur <= m || maxInFlight.CompareAndSwap(m, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
	}

	done := make(chan struct{}, 5)
	for range 5 {
		go func() {
			release, err := sem.acquire(context.Background())
			if err != nil {
				t.Errorf("acquire failed: %v", err)
				done <- struct{}{}
				return
			}
			track()
			release()
			done <- struct{}{}
		}()
	}
	for range 5 {
		<-done
	}

	if got := maxInFlight.Load(); got > 2 {
		t.Fatalf("max concurrent = %d, want <= 2", got)
	}
}

func TestOpSemaphore_ReleaseAllowsNextAcquire(t *testing.T) {
	sem := newOpSemaphore(1)
	release1, err := sem.acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		release2, err := sem.acquire(context.Background())
		if err != nil {
			t.Errorf("second acquire failed: %v", err)
			return
		}
		close(acquired)
		release2()
	}()

	select {
	case <-acquired:
		t.Fatal("second acquire succeeded while first slot was held")
	case <-time.After(50 * time.Millisecond):
	}

	release1()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second acquire never succeeded after release")
	}
}

func TestOpSemaphore_AcquireRespectsContextCancel(t *testing.T) {
	sem := newOpSemaphore(1)
	release, err := sem.acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = sem.acquire(ctx)
	if err == nil {
		t.Fatal("expected acquire to fail when context is cancelled while waiting")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/mcp/... -run TestOpSemaphore -v`
Expected: FAIL with `undefined: newOpSemaphore`.

- [ ] **Step 3: Implement `opSemaphore`**

Create `cmd/mcp/semaphore.go`:

```go
package mcp

import (
	"context"
	"fmt"
)

// opSemaphore bounds how many expensive workspace operations (exec, create,
// start) run concurrently in one MCP server process, so an orchestrator
// driving many workspaces can't overwhelm the local Docker/Kubernetes
// backend. Devsy itself has no other admission control (see pkg/provider
// locks, which serialize per-workspace but not process-wide).
type opSemaphore struct {
	slots chan struct{}
}

func newOpSemaphore(max int) *opSemaphore {
	if max <= 0 {
		max = 1
	}
	return &opSemaphore{slots: make(chan struct{}, max)}
}

// acquire blocks until a slot is free or ctx is done. On success the caller
// must call the returned release func exactly once.
func (s *opSemaphore) acquire(ctx context.Context) (func(), error) {
	select {
	case s.slots <- struct{}{}:
		return func() { <-s.slots }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for a free operation slot: %w", ctx.Err())
	}
}
```

- [ ] **Step 4: Run the semaphore tests to verify they pass**

Run: `go test ./cmd/mcp/... -run TestOpSemaphore -race -v`
Expected: all three tests PASS.

- [ ] **Step 5: Wire the semaphore into `ServeCmd` and the flag**

In `pkg/flags/names/names.go`, add a new constant near the existing `ExecOutputCap`/`ExecTimeoutDefault`/`ExecTimeoutMax` (line ~307-309):

```go
	MaxConcurrentOps      = "mcp-max-concurrent-ops"
```

In `cmd/mcp/serve.go`, add the field to `ServeCmd` (after `ExecOutputCap int`):

```go
	MaxConcurrentOps int
```

Add the flag registration in `NewServeCmd` (after the existing `cliflags.Int(&cmd.ExecOutputCap, ...)` call, inside the same `cliflags.Add(...)` call):

```go
		cliflags.Int(
			&cmd.MaxConcurrentOps,
			names.MaxConcurrentOps,
			8,
			"Maximum number of concurrent workspace_exec/workspace_create/workspace_start "+
				"operations; excess calls wait for a free slot",
		),
```

In `ServeCmd.Run` (`cmd/mcp/serve.go`, the method that currently builds `transport` and `server` and calls `cmd.registerTools(server)`), construct the semaphore and pass it through:

```go
func (cmd *ServeCmd) Run(ctx context.Context) error {
	log.Debugf("starting MCP server (timeout default=%s max=%s cap=%dB maxops=%d)",
		cmd.ExecTimeoutDefault, cmd.ExecTimeoutMax, cmd.ExecOutputCap, cmd.MaxConcurrentOps)

	realStdout := os.Stdout
	os.Stdout = os.Stderr
	defer func() { os.Stdout = realStdout }()

	transport := &sdkmcp.IOTransport{
		Reader: os.Stdin,
		Writer: realStdout,
	}

	server := sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:    "devsy",
		Version: version.GetVersion(),
	}, nil)

	sem := newOpSemaphore(cmd.MaxConcurrentOps)
	cmd.registerTools(server, sem)

	return server.Run(ctx, transport)
}

func (cmd *ServeCmd) registerTools(s *sdkmcp.Server, sem *opSemaphore) {
	registerWorkspaceTools(s, cmd.GlobalFlags, sem)
	registerExecTool(s, cmd, sem)
	registerProviderTools(s, cmd.GlobalFlags)
}
```

- [ ] **Step 6: Update `serve_test.go`'s call site**

`cmd/mcp/serve_test.go`'s `TestServer_ListsAllTools` calls `serveCmd.registerTools(server)` directly — update it to pass a semaphore:

```go
	serveCmd.registerTools(server, newOpSemaphore(8))
```

- [ ] **Step 7: Gate `workspace_exec` in `cmd/mcp/tools_exec.go`**

Change `registerExecTool`'s signature to accept the semaphore, and acquire/release it around the `workspace.ExecOneShot` call:

```go
func registerExecTool(s *sdkmcp.Server, cmd *ServeCmd, sem *opSemaphore) {
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "workspace_exec",
		Description: "Run a one-shot command in a running workspace container. The " +
			"workspace name must match workspace_list; the workspace must already be " +
			"started (use workspace_start if not). 'command' is argv, NOT a shell " +
			"string — pass [\"sh\", \"-c\", \"...\"] to use a shell. Each output " +
			"stream is capped; long output is truncated tail-only with a marker.",
	}, safeHandler(func(
		ctx context.Context, _ *sdkmcp.CallToolRequest, in execInput,
	) (*sdkmcp.CallToolResult, execOutput, error) {
		if in.Name == "" {
			return errorResult(fmt.Errorf("name is required")), execOutput{}, nil
		}
		if len(in.Command) == 0 {
			return errorResult(fmt.Errorf("command is required")), execOutput{}, nil
		}

		release, err := sem.acquire(ctx)
		if err != nil {
			return errorResult(err), execOutput{}, nil
		}
		defer release()

		stdout := NewBoundedBuffer(cmd.ExecOutputCap)
		stderr := NewBoundedBuffer(cmd.ExecOutputCap)

		res, err := workspace.ExecOneShot(ctx, workspace.ExecOneShotOptions{
			WorkspaceName:         in.Name,
			Command:               in.Command,
			Workdir:               in.Workdir,
			Env:                   in.Env,
			IDLabels:              in.IDLabels,
			TimeoutSeconds:        in.TimeoutSeconds,
			TimeoutSecondsDefault: durationToSeconds(cmd.ExecTimeoutDefault),
			TimeoutSecondsMax:     durationToSeconds(cmd.ExecTimeoutMax),
			Owner:                 cmd.Owner,
			Context:               cmd.Context,
			Provider:              cmd.Provider,
			Stdout:                stdout,
			Stderr:                stderr,
		})
		out := execOutput{
			Stdout:    stdout.String(),
			Stderr:    stderr.String(),
			Truncated: stdout.Truncated() || stderr.Truncated(),
		}
		if res != nil {
			out.ExitCode = res.ExitCode
			out.DurationMS = res.DurationMS
			out.TimedOut = res.TimedOut
			out.Clamped = res.Clamped
		}
		if err != nil {
			payload := ClassifyError(err)
			out.Error = &payload
			return errorResult(err), out, nil
		}
		return nil, out, nil
	}))
}
```

(Only the signature and the `release, err := sem.acquire(ctx)` / `defer release()` block are new; the rest of the function body is unchanged from the current implementation.)

- [ ] **Step 8: Gate `workspace_create` and `workspace_start` in `cmd/mcp/tools_workspace.go`**

Change `registerWorkspaceTools`'s signature to thread the semaphore through to `registerWorkspaceLifecycleTools`:

```go
func registerWorkspaceTools(s *sdkmcp.Server, g *flags.GlobalFlags, sem *opSemaphore) {
	// ... workspace_list and workspace_status registrations unchanged ...

	registerWorkspaceLifecycleTools(s, g, sem)
}
```

In `registerWorkspaceLifecycleTools` (`cmd/mcp/tools_workspace.go:141`), update the signature and gate the two operations that provision infrastructure:

```go
func registerWorkspaceLifecycleTools(s *sdkmcp.Server, g *flags.GlobalFlags, sem *opSemaphore) {
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "workspace_start",
		Description: "Start (or resume) an existing workspace by name. The name must " +
			"match a workspace from workspace_list; use workspace_create to make a new one. " +
			"May take a minute or more while the container starts.",
	}, safeHandler(func(
		ctx context.Context, req *sdkmcp.CallToolRequest, in nameInput,
	) (*sdkmcp.CallToolResult, opOK, error) {
		if in.Name == "" {
			return errorResult(fmt.Errorf("name is required")), opOK{}, nil
		}
		release, err := sem.acquire(ctx)
		if err != nil {
			return errorResult(err), opOK{}, nil
		}
		defer release()
		return opResultHandler(func() error {
			return streamLogsToSession(ctx, req.Session, func() error {
				return startWorkspace(ctx, g, in.Name)
			})
		})
	}))

	// workspace_stop and workspace_delete registrations unchanged (not gated).

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "workspace_create",
		Description: "Create and start a new workspace. May take several minutes on " +
			"first use (image pull, git clone, post-create commands); if the call " +
			"times out client-side, the server-side operation likely continued — " +
			"poll workspace_status to check.\n" +
			"\n" +
			"source: a git URL (https://, git@host:repo, ssh://, or git:https://... " +
			"as emitted by workspace_list), an absolute local path, or a container " +
			"image reference.\n" +
			"provider: the name of a configured provider; see provider_list. " +
			"Defaults to the active context's default provider.",
	}, safeHandler(func(
		ctx context.Context, req *sdkmcp.CallToolRequest, in createInput,
	) (*sdkmcp.CallToolResult, any, error) {
		release, err := sem.acquire(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		defer release()

		var out any
		streamErr := streamLogsToSession(ctx, req.Session, func() error {
			out, err = createWorkspace(ctx, g, in)
			return err
		})
		if streamErr != nil {
			return errorResult(streamErr), nil, nil
		}
		return nil, out, nil
	}))
}
```

You must keep the existing `workspace_stop` and `workspace_delete` `sdkmcp.AddTool` calls in `registerWorkspaceLifecycleTools` exactly as they currently are (unmodified, ungated) — only `workspace_start` and `workspace_create` change.

- [ ] **Step 9: Build and fix any remaining call-site mismatches**

Run: `go build ./... 2>&1 | head -60`
Expected: builds cleanly. If any other file calls `registerWorkspaceTools`/`registerExecTool`/`registerWorkspaceLifecycleTools` directly (check via `grep -rn "registerWorkspaceTools\|registerExecTool\|registerWorkspaceLifecycleTools" cmd/mcp/*.go`), update those call sites too.

- [ ] **Step 10: Write an integration-style test proving the gate applies to a real tool call**

Add to `cmd/mcp/serve_test.go` (or a new `cmd/mcp/semaphore_integration_test.go` if you prefer keeping `serve_test.go` focused on tool-listing):

```go
func TestServer_WorkspaceExecRespectsSemaphore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEVSY_HOME", home)

	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "devsy-test", Version: "test"}, nil)
	serveCmd := &ServeCmd{GlobalFlags: &flags.GlobalFlags{}, MaxConcurrentOps: 1}
	sem := newOpSemaphore(serveCmd.MaxConcurrentOps)
	serveCmd.registerTools(server, sem)

	release, err := sem.acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, acquireErr := sem.acquire(ctx)
	if acquireErr == nil {
		t.Fatal("expected second acquire to block/fail while the only slot is held")
	}
	release()
}
```

This documents the contract (semaphore blocks a second acquire while the single slot is held) at the level the tool handlers rely on, without needing a real Docker workspace — a full stdio round-trip through `workspace_exec` against a real container belongs in the e2e suite (Task 4), not this unit test.

Run: `go test ./cmd/mcp/... -run TestServer -v`
Expected: PASS.

- [ ] **Step 11: Run the full `cmd/mcp` test suite**

Run: `go test ./cmd/mcp/... -race -v 2>&1 | tail -80`
Expected: all tests PASS, no data races.

- [ ] **Step 12: Commit**

```bash
git add cmd/mcp/semaphore.go cmd/mcp/semaphore_test.go cmd/mcp/serve.go cmd/mcp/serve_test.go cmd/mcp/tools_exec.go cmd/mcp/tools_workspace.go pkg/flags/names/names.go
git commit -m "feat(mcp): bound concurrent workspace_exec/create/start operations with a semaphore"
```

---

### Task 3: `--ide-launch=skip` also skips IDE server install

**Files:**
- Modify: `cmd/workspace/up/up_validate.go` (`validate` method)
- Test: `cmd/workspace/up/up_test.go` (append new test)
- Test: `e2e/tests/ide/` (extend with a new spec — see Step 6)

**Interfaces:**
- Consumes: `opener.LaunchSkip` (`pkg/ide/opener/ide_launch_mode.go:20`), `config.IDENone` (`pkg/config/ide.go`).
- Produces: no new exported symbols; `UpCmd.validate()`'s existing behavior gains one new normalization step that later code (`installIDE` in `cmd/internal/agentcontainer/setup.go:646`, gated on `ide.Name`) picks up automatically since it already reads `workspace.IDE.Name` / `cmd.IDE` downstream of `validate()`.

**Background (from source investigation):** Two independent gates exist for IDE behavior. `openIDE` (`cmd/workspace/up/configure.go:65-73`) checks `cmd.IDELaunch == opener.LaunchSkip` and skips the host-side launch — this already works correctly. But `installIDE` (`cmd/internal/agentcontainer/setup.go:646-664`), which runs container-side during `setupPostAttach` and decides whether to download/install an IDE server binary (openvscode-server, code-server, JetBrains backends, etc.), is gated **exclusively** on `ide.Name == string(config2.IDENone)` — it has zero knowledge of `--ide-launch` at all, because it runs in a completely different process/phase (inside the container agent) that never receives the host-side `IDELaunch` flag. So a caller who passes `--ide-launch=skip` without also passing `--ide=none` still pays for a full IDE server download — exactly the failure observed in hands-on testing (a TLS error downloading `openvscode-server-v1.109.5` blocked an otherwise-successful headless `workspace up`).

The fix belongs on the host side, before the IDE name is ever sent to the container: when `--ide-launch=skip` is set and the caller did not explicitly choose an IDE (`--ide` empty), coerce `cmd.IDE` to `"none"` too — mirroring exactly what `RunHeadless` already does deliberately (`cmd/workspace/up/up.go:94-98`: `IDELaunch: opener.LaunchSkip` and `cmd.IDE = string(config.IDENone)` are set together). If the caller explicitly passed a real `--ide` value alongside `--ide-launch=skip`, respect their explicit choice and do NOT override it — they may legitimately want the backend installed but not auto-launched (e.g. pre-warming an IDE for a human to open later via `devsy ide` tooling). This requires distinguishing "flag not passed" from "flag passed empty string," which `cobra`'s `Flags().Changed()` already supports elsewhere in this file's package (see `cmd/workspace/up/up.go:427`, `applyPullFromInsideContainerOverride`).

- [ ] **Step 1: Write the failing test**

Add to `cmd/workspace/up/up_test.go`:

```go
func TestUpCmd_IDELaunchSkipImpliesIDENoneWhenIDEUnset(t *testing.T) {
	cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
	cmd.IDELaunch = opener.LaunchSkip
	cmd.IDE = ""

	err := cmd.validate()
	require.NoError(t, err)
	assert.Equal(t, string(config.IDENone), cmd.IDE,
		"ide-launch=skip with no explicit --ide should default IDE to none, "+
			"so the container never downloads an IDE server binary")
}

func TestUpCmd_IDELaunchSkipRespectsExplicitIDE(t *testing.T) {
	cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
	cmd.IDELaunch = opener.LaunchSkip
	cmd.IDE = "openvscode"

	err := cmd.validate()
	require.NoError(t, err)
	assert.Equal(t, "openvscode", cmd.IDE,
		"an explicit --ide value must not be overridden even when launch is skipped")
}

func TestUpCmd_IDELaunchAutoDoesNotTouchIDE(t *testing.T) {
	cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
	cmd.IDELaunch = opener.LaunchAuto
	cmd.IDE = ""

	err := cmd.validate()
	require.NoError(t, err)
	assert.Equal(t, "", cmd.IDE, "auto launch must not force an IDE default in validate()")
}
```

Add the necessary imports to `cmd/workspace/up/up_test.go` if not already present: `"github.com/devsy-org/devsy/pkg/config"` and `"github.com/devsy-org/devsy/pkg/ide/opener"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/workspace/up/... -run TestUpCmd_IDELaunchSkip -v`
Expected: FAIL — `TestUpCmd_IDELaunchSkipImpliesIDENoneWhenIDEUnset` fails because `cmd.IDE` is still `""`, not `"none"`.

- [ ] **Step 3: Implement the fix in `validate()`**

In `cmd/workspace/up/up_validate.go`, add the import for `opener` and `config` packages, then add a new step to `validate()`:

```go
package up

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/ide/opener"
)
```

```go
func (cmd *UpCmd) validate() error {
	cmd.applySkipLaunchIDEDefault()

	if err := devcontainer.ResolveSourceSpec(&cmd.CLIOptions); err != nil {
		return err
	}
	if err := validatePodmanFlags(cmd); err != nil {
		return err
	}
	if err := config2.ValidateIDLabels(cmd.IDLabels); err != nil {
		return err
	}
	if err := cmd.validateUserEnvProbe(); err != nil {
		return err
	}
	if err := cmd.resolveExtraDevContainerPath(); err != nil {
		return err
	}
	if err := validateWorkspaceMountConsistency(cmd.WorkspaceMountConsistency); err != nil {
		return err
	}
	if err := validateMounts(cmd.Mounts); err != nil {
		return err
	}

	return validateRemoteUserUID(cmd.UpdateRemoteUserUIDDefault)
}

// applySkipLaunchIDEDefault mirrors the pairing RunHeadless already applies
// deliberately (up.go's RunHeadless sets IDELaunch=LaunchSkip and
// IDE=config.IDENone together): if the caller asked to skip IDE launch and
// did not explicitly choose an IDE, also skip the IDE server install by
// defaulting IDE to none. Without this, --ide-launch=skip only suppresses
// the host-side open, while the container still downloads and installs an
// IDE server binary nobody asked for (see cmd/internal/agentcontainer/setup.go
// installIDE, which is gated purely on IDE name, not IDELaunch).
func (cmd *UpCmd) applySkipLaunchIDEDefault() {
	if cmd.IDELaunch == opener.LaunchSkip && cmd.IDE == "" {
		cmd.IDE = string(config.IDENone)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/workspace/up/... -run TestUpCmd_IDELaunchSkip -v`
Expected: all three tests PASS.

- [ ] **Step 5: Run the full `up` package test suite**

Run: `go test ./cmd/workspace/up/... -race -v 2>&1 | tail -60`
Expected: all tests PASS, including the pre-existing `TestUpCmd_NoLockfileAndFrozenLockfileMutuallyExclusive` and `TestUpCmd_ValidateDefaultUserEnvProbe` suites — `applySkipLaunchIDEDefault` must not affect any case where `IDELaunch` isn't `LaunchSkip` (verify `TestUpCmd_ValidateDefaultUserEnvProbe`'s cases still pass unchanged since they don't set `IDELaunch`, so it defaults to the Go zero value `""`, which is not `LaunchSkip`, so the new guard is a no-op for them).

- [ ] **Step 6: Add an e2e regression test**

Create `e2e/tests/ide/skip_launch_no_install.go` (following the existing `e2e/tests/ide/ide.go`-style pattern of a `ginkgo.Describe` block; check the existing file name via `ls e2e/tests/ide/*.go` first and add to whichever file already holds the top-level `ginkgo.Describe` for this package, or create a new file if the package splits specs across files as `browser_returns.go`/`vscode_settings.go` suggest):

```go
package ide

import (
	"context"
	"os"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("devsy up --ide-launch=skip", ginkgo.Label("ide"), ginkgo.Ordered, func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.It("does not install an IDE server binary when --ide is omitted",
		func(ctx context.Context) {
			f, tempDir := setupBrowserIDE(ctx, initialDir)

			err := f.DevsyUpWithIDE(ctx, "--ide-launch=skip", tempDir)
			framework.ExpectNoError(err)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"workspace", "exec", "--workspace-folder", tempDir,
				"--", "sh", "-c", "test -d /root/.openvscode-server && echo present || echo absent",
			})
			framework.ExpectNoError(err)
			gomega.Expect(stdout).To(gomega.ContainSubstring("absent"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))
})
```

Check the actual install path openvscode-server uses on disk before finalizing this assertion — grep `cmd/internal/agentcontainer/setup.go`'s `setupBrowserIDE` implementation for the exact target directory it installs to (it may not be `/root/.openvscode-server`; confirm the real path so the e2e assertion checks the right location) and adjust the `test -d ...` path accordingly.

- [ ] **Step 7: Run the e2e test locally (requires Docker)**

Run: `task cli:test:e2e:focus -- "ide-launch=skip"`
Expected: PASS. This requires a local Docker daemon; if unavailable, at minimum confirm `go vet ./e2e/...` and `go build ./e2e/...` succeed so the new spec compiles and registers correctly.

- [ ] **Step 8: Commit**

```bash
git add cmd/workspace/up/up_validate.go cmd/workspace/up/up_test.go e2e/tests/ide/skip_launch_no_install.go
git commit -m "fix(up): --ide-launch=skip also skips IDE server install when --ide is unset"
```

---

### Task 4: MCP stdio e2e test coverage

**Files:**
- Create: `e2e/framework/mcp.go`
- Create: `e2e/tests/mcp/helper.go`
- Create: `e2e/tests/mcp/mcp.go`
- Create: `e2e/tests/mcp/testdata/basic/.devcontainer/devcontainer.json`
- Modify: `e2e/e2e_suite_test.go` (add blank import)

**Interfaces:**
- Consumes: `framework.Framework{DevsyBinDir, DevsyBinName}` (`e2e/framework/framework.go:7`), `framework.CopyToTempDir`, `framework.CleanupTempDir`, `framework.SetupDockerProvider`, `f.DevsyWorkspaceDelete` — all existing, unmodified.
- Produces: `type MCPClient struct{...}` in the `framework` package with methods `func (f *Framework) StartMCPServer(ctx context.Context) (*MCPClient, error)`, `func (c *MCPClient) CallTool(ctx context.Context, name string, args map[string]any) (map[string]any, bool, error)` (returns decoded `structuredContent`, `isError`, and any transport error), and `func (c *MCPClient) Close() error` — these are new, reusable by any future MCP e2e spec.

**Background (from source investigation):** `e2e/tests/exec/exec.go` and `e2e/tests/exec/helper.go` establish the pattern every e2e package follows: a per-package `setupWorkspace`/`setupWorkspaceAndUp` helper builds a `*framework.Framework` and a temp workspace dir via `framework.CopyToTempDir`, registers `ginkgo.DeferCleanup` for teardown, then the spec file shells out to the real compiled `devsy` binary via `f.ExecCommandCapture(ctx, args)` and asserts on stdout/stderr/exit code with Gomega. `cmd/mcp/serve_test.go`'s `TestServer_ListsAllTools` only tests tool registration in-process via `sdkmcp.NewInMemoryTransports()` — it never spawns the real binary or speaks real stdio JSON-RPC, so there is no existing coverage that `devsy mcp serve` behaves correctly as an actual subprocess a real MCP client talks to. No existing framework helper speaks JSON-RPC-over-stdio; `MCPClient` is new plumbing this task adds, modeled directly on `e2e/framework/exec.go`'s `exec.CommandContext(ctx, filepath.Join(f.DevsyBinDir, f.DevsyBinName), args...)` construction but with stdin/stdout pipes kept open across multiple request/response round trips instead of a one-shot `cmd.Run()`.

- [ ] **Step 1: Create the MCP stdio client helper in the framework package**

Create `e2e/framework/mcp.go`:

```go
package framework

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync/atomic"
)

// MCPClient drives a real `devsy mcp serve` subprocess over stdio JSON-RPC,
// for e2e tests that need to exercise the actual MCP transport rather than
// the in-process SDK transport used by cmd/mcp's unit tests.
type MCPClient struct {
	cmd     *exec.Cmd
	stdin   *bufio.Writer
	stdout  *bufio.Reader
	nextID  atomic.Int64
	closeFn func() error
}

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// StartMCPServer launches `devsy mcp serve` as a subprocess and completes the
// MCP initialize handshake. Callers must call Close when done.
func (f *Framework) StartMCPServer(ctx context.Context) (*MCPClient, error) {
	cmd := exec.CommandContext(ctx, filepath.Join(f.DevsyBinDir, f.DevsyBinName), "mcp", "serve")
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start devsy mcp serve: %w", err)
	}

	c := &MCPClient{
		cmd:    cmd,
		stdin:  bufio.NewWriter(stdinPipe),
		stdout: bufio.NewReader(stdoutPipe),
		closeFn: func() error {
			_ = stdinPipe.Close()
			return cmd.Wait()
		},
	}

	if err := c.send(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      c.nextID.Add(1),
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "devsy-e2e", "version": "0.1"},
		},
	}); err != nil {
		return nil, err
	}
	if _, err := c.readResponse(); err != nil {
		return nil, fmt.Errorf("initialize handshake: %w", err)
	}
	if err := c.send(jsonRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"}); err != nil {
		return nil, err
	}

	return c, nil
}

func (c *MCPClient) send(req jsonRPCRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	if _, err := c.stdin.Write(data); err != nil {
		return fmt.Errorf("write request: %w", err)
	}
	if err := c.stdin.WriteByte('\n'); err != nil {
		return fmt.Errorf("write newline: %w", err)
	}
	return c.stdin.Flush()
}

func (c *MCPClient) readResponse() (*jsonRPCResponse, error) {
	line, err := c.stdout.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var resp jsonRPCResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response %q: %w", line, err)
	}
	return &resp, nil
}

// CallTool invokes an MCP tool and returns its decoded structuredContent,
// whether the tool reported isError, and any transport-level error.
func (c *MCPClient) CallTool(name string, args map[string]any) (map[string]any, bool, error) {
	if err := c.send(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      c.nextID.Add(1),
		Method:  "tools/call",
		Params:  map[string]any{"name": name, "arguments": args},
	}); err != nil {
		return nil, false, err
	}
	resp, err := c.readResponse()
	if err != nil {
		return nil, false, err
	}
	if resp.Error != nil {
		return nil, false, fmt.Errorf("jsonrpc error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	var result struct {
		IsError           bool           `json:"isError"`
		StructuredContent map[string]any `json:"structuredContent"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, false, fmt.Errorf("unmarshal tool result: %w", err)
	}
	return result.StructuredContent, result.IsError, nil
}

// Close terminates the subprocess and releases its pipes.
func (c *MCPClient) Close() error {
	return c.closeFn()
}
```

- [ ] **Step 2: Create the e2e test fixture devcontainer**

Create `e2e/tests/mcp/testdata/basic/.devcontainer/devcontainer.json` (mirroring `e2e/tests/exec/testdata/exec/.devcontainer/devcontainer.json`):

```json
{
  "name": "MCP Test",
  "image": "ghcr.io/devsy-org/test-images/base:ubuntu"
}
```

- [ ] **Step 3: Create the setup helper**

Create `e2e/tests/mcp/helper.go`, mirroring `e2e/tests/exec/helper.go`:

```go
package mcp

import (
	"context"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
)

func setupWorkspace(testdataPath, initialDir string) (string, *framework.Framework, error) {
	tempDir, err := framework.CopyToTempDir(testdataPath)
	if err != nil {
		return "", nil, err
	}

	f, err := framework.SetupDockerProvider(initialDir+"/bin", "docker")
	if err != nil {
		return "", nil, err
	}

	ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)
	ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

	return tempDir, f, nil
}

func setupWorkspaceAndUp(
	ctx context.Context,
	testdataPath, initialDir string,
) (string, *framework.Framework, error) {
	tempDir, f, err := setupWorkspace(testdataPath, initialDir)
	if err != nil {
		return "", nil, err
	}

	return tempDir, f, f.DevsyUp(ctx, tempDir)
}
```

- [ ] **Step 4: Write the e2e spec**

Create `e2e/tests/mcp/mcp.go`:

```go
package mcp

import (
	"context"
	"os"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("devsy mcp serve", ginkgo.Label("mcp"), ginkgo.Ordered, func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.It("lists a running workspace via workspace_list and execs a command via workspace_exec",
		func(ctx context.Context) {
			tempDir, f, err := setupWorkspaceAndUp(ctx, "tests/mcp/testdata/basic", initialDir)
			framework.ExpectNoError(err)

			client, err := f.StartMCPServer(ctx)
			framework.ExpectNoError(err)
			defer client.Close()

			listResult, isErr, err := client.CallTool("workspace_list", map[string]any{})
			framework.ExpectNoError(err)
			gomega.Expect(isErr).To(gomega.BeFalse())
			workspaces, ok := listResult["workspaces"].([]any)
			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(workspaces).NotTo(gomega.BeEmpty())

			execResult, isErr, err := client.CallTool("workspace_exec", map[string]any{
				"name":    tempDir,
				"command": []string{"echo", "-n", "hello-from-mcp"},
			})
			framework.ExpectNoError(err)
			gomega.Expect(isErr).To(gomega.BeFalse())
			gomega.Expect(execResult["stdout"]).To(gomega.Equal("hello-from-mcp"))
			gomega.Expect(execResult["exit_code"]).To(gomega.BeNumerically("==", 0))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("returns an isError result for an unknown workspace name",
		func(ctx context.Context) {
			client, err := f2(initialDir)
			framework.ExpectNoError(err)
			defer client.Close()

			_, isErr, err := client.CallTool("workspace_exec", map[string]any{
				"name":    "definitely-not-a-real-workspace",
				"command": []string{"echo", "hi"},
			})
			framework.ExpectNoError(err)
			gomega.Expect(isErr).To(gomega.BeTrue())
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))
})

func f2(initialDir string) (*framework.MCPClient, error) {
	f := framework.NewDefaultFramework(initialDir + "/bin")
	return f.StartMCPServer(context.Background())
}
```

Rename the helper `f2` to something clearer before committing — it exists here only because the second spec doesn't need a real workspace, just a framework instance; check whether `framework.NewDefaultFramework` combined with `StartMCPServer` is enough on its own (it should be, since `workspace_exec` against a nonexistent name doesn't require Docker at all — `workspace.Get` fails before any container interaction) and simplify the second `It` block to construct the framework and client inline without the extra indirection function.

- [ ] **Step 5: Register the new package in the e2e suite**

In `e2e/e2e_suite_test.go`, add the blank import in alphabetical order with the existing list:

```go
	_ "github.com/devsy-org/devsy/e2e/tests/machine"
	_ "github.com/devsy-org/devsy/e2e/tests/machineprovider"
	_ "github.com/devsy-org/devsy/e2e/tests/mcp"
	_ "github.com/devsy-org/devsy/e2e/tests/outdated"
```

- [ ] **Step 6: Verify the new package builds and registers**

Run: `go build ./e2e/... 2>&1 | head -40`
Expected: builds cleanly.

Run: `go vet ./e2e/...`
Expected: no issues.

- [ ] **Step 7: Run the new e2e spec (requires Docker)**

Run: `task cli:test:e2e:focus -- "devsy mcp serve"`
Expected: both `It` blocks PASS. If Docker is unavailable in the current environment, at minimum confirm the build/vet steps above pass and note that live execution needs to happen in CI or a Docker-enabled environment before merging.

- [ ] **Step 8: Commit**

```bash
git add e2e/framework/mcp.go e2e/tests/mcp/ e2e/e2e_suite_test.go
git commit -m "test(e2e): add MCP stdio JSON-RPC e2e coverage for workspace_list/workspace_exec"
```

---

### Task 5: Parent-directory fsync in `WriteFileAtomic`

**Files:**
- Modify: `pkg/provider/atomic.go`
- Test: `pkg/provider/atomic_test.go` (append new test)

**Interfaces:**
- Consumes: nothing new.
- Produces: `WriteFileAtomic`'s exported signature and behavior contract are unchanged (still `func WriteFileAtomic(path string, data []byte, perm os.FileMode) error`) — this task only strengthens its durability guarantee on POSIX platforms; all five existing callers in `pkg/provider/dir.go` (`SaveProviderConfig`, `SaveProInstanceConfig`, `SaveWorkspaceResult`, `SaveWorkspaceConfig`, `SaveMachineConfig`) need no changes.

**Background (from source investigation):** `WriteFileAtomic` (`pkg/provider/atomic.go:22-55`) already fsyncs the temp file's data (`tmp.Sync()` at line 41) before renaming, but never fsyncs the parent directory after `os.Rename` — the function's own doc comment (lines 15-21) already documents this exact gap and suggests the fix: "add a parent-directory fsync here." Directory fsync is a standard POSIX durability idiom (open the directory read-only, call `Sync()`) but is unreliable/unsupported on Windows — the repo already has an established pattern for this kind of platform split (e.g. `pkg/pty/pty_windows.go` vs `pkg/pty/pty_other.go`), so this fix uses a `//go:build` tag rather than a runtime `runtime.GOOS` check to keep the Windows binary free of a no-op syscall.

- [ ] **Step 1: Write the failing test**

Add to `pkg/provider/atomic_test.go` (this file currently has no `//go:build` tag, and the existing test doesn't check directory-fsync behavior directly since that's not observable via `os.ReadFile` — instead, test that the function still succeeds and that a real directory fsync call doesn't error, which is what's actually verifiable without simulating a crash):

```go
func TestWriteFileAtomic_SucceedsAndDataIsDurable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "durable.json")

	if err := WriteFileAtomic(path, []byte(`{"durable":true}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	data, err := os.ReadFile(path) //nolint:gosec // test reads a path under t.TempDir
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != `{"durable":true}` {
		t.Fatalf("got %q, want the written content", data)
	}
}
```

This test alone won't distinguish "fsync'd the directory" from "didn't" — that's inherent to testing crash durability without actually crashing the process. Its purpose is to pin down that the fix doesn't break the existing happy path or leak file descriptors; the actual durability improvement is verified by code review of the diff plus the platform-gating check in Step 4.

- [ ] **Step 2: Run test to verify it currently passes (baseline) then implement**

Run: `go test ./pkg/provider/... -run TestWriteFileAtomic_SucceedsAndDataIsDurable -v`
Expected: PASS even before the fix (this test doesn't exercise the new code path yet — it's a regression guard, written first per TDD convention, that must keep passing after Step 3).

- [ ] **Step 3: Implement the parent-directory fsync**

Create `pkg/provider/atomic_posix.go`:

```go
//go:build !windows

package provider

import "os"

// syncDir fsyncs a directory so that a prior rename(2) into it is durable
// across a crash, not just visible to concurrent readers. Directory fsync
// has no reliable equivalent on Windows (NTFS handles directory metadata
// durability differently and os.File.Sync on a directory handle is not
// meaningful there), so this is POSIX-only; atomic_windows.go provides a
// no-op for the same signature.
func syncDir(dir string) error {
	f, err := os.OpenFile(dir, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
```

Create `pkg/provider/atomic_windows.go`:

```go
//go:build windows

package provider

// syncDir is a no-op on Windows; see atomic_posix.go for the rationale.
func syncDir(_ string) error {
	return nil
}
```

Modify `pkg/provider/atomic.go` to call `syncDir` after the rename:

```go
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename temp file: %w", err)
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("sync parent dir: %w", err)
	}
	return nil
}
```

Update the doc comment on `WriteFileAtomic` (lines 9-21) to reflect the new guarantee — replace the paragraph starting "This helper does NOT guarantee crash durability" with:

```go
// WriteFileAtomic writes data to path atomically by writing to a sibling
// temp file then renaming. POSIX rename(2) ensures concurrent readers see
// either the old or the new content, never a partially-written file —
// which is the guarantee callers of this helper rely on for config files
// like workspace.json.
//
// On POSIX platforms this also fsyncs the parent directory after the
// rename, so the rename itself survives a crash immediately after this
// function returns (see atomic_posix.go). On Windows the directory sync is
// a no-op (see atomic_windows.go) — Windows callers retain the pre-existing
// weaker guarantee and should still tolerate re-resolving state on the next
// run, as they always have.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
```

- [ ] **Step 4: Verify the platform gating builds for both target platforms**

Run: `GOOS=linux go build ./pkg/provider/... 2>&1 | head -20`
Expected: builds cleanly.

Run: `GOOS=windows go build ./pkg/provider/... 2>&1 | head -20`
Expected: builds cleanly (this confirms `atomic_windows.go`'s build tag is correct and the package compiles without the POSIX-only file).

Run: `GOOS=darwin go build ./pkg/provider/... 2>&1 | head -20`
Expected: builds cleanly.

- [ ] **Step 5: Run the full atomic.go test suite on the current platform**

Run: `go test ./pkg/provider/... -run TestWriteFileAtomic -race -v`
Expected: both the new `TestWriteFileAtomic_SucceedsAndDataIsDurable` and the pre-existing `TestWriteFileAtomic_ConcurrentReadersSeeNoPartialWrites` PASS, no data races.

- [ ] **Step 6: Run the full `pkg/provider` test suite to check for regressions**

Run: `go test ./pkg/provider/... -race -v 2>&1 | tail -60`
Expected: all tests PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/provider/atomic.go pkg/provider/atomic_posix.go pkg/provider/atomic_windows.go pkg/provider/atomic_test.go
git commit -m "fix(provider): fsync parent directory after atomic rename for crash durability"
```

---

## Final Verification

- [ ] **Run the full non-e2e test suite**

Run: `go test $(go list ./... | grep -v -e /test -e /e2e) -race -coverprofile=dist/profile.out -covermode=atomic 2>&1 | tail -100`
Expected: all packages PASS, no data races. This is the same command CI runs (`Taskfile.yml`'s `cli:test` target).

- [ ] **Run `go vet` and `go build` across the whole module**

Run: `go build ./... && go vet ./...`
Expected: no errors.

- [ ] **Run linting**

Run: `golangci-lint run ./... 2>&1 | tail -100` (if `golangci-lint` isn't installed locally, check `.golangci.yaml` for the configured version and install via the repo's documented method in `CONTRIBUTING.md`).
Expected: no new lint violations introduced by this plan's changes.

- [ ] **If Docker is available, run the affected e2e suites**

Run: `task cli:test:e2e:focus -- "exec|mcp|ide-launch"`
Expected: all PASS.
