package up

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/agent"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/driver/drivercreate"
	"github.com/devsy-org/devsy/pkg/encoding"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	microsandboxCancelBound    = 10 * time.Second
	microsandboxCancelSentinel = "source-sentinel"
	microsandboxCancelShell    = "/bin/sh"
)

type microsandboxLogCancelState struct {
	Status string `json:"status"`
}

// The child blocks on its own FIFO; the parent either waits for it or reads the
// caller's stdin. Neither process has a self-exit timer. The nonce is in both
// argv vectors, and readiness includes each process's kernel start time.
const microsandboxCancelScript = `set -eu
nonce=$1
mode=$2
start_time() { sed 's/^.*) //' "/proc/$1/stat" | awk '{print $20}'; }
if [ "$mode" = child ]; then
  fifo="/root/$nonce.fifo"
  mkfifo "$fifo"
  exec 9<>"$fifo"
  printf 'READY %s child %s %s\n' "$nonce" "$$" "$(start_time $$)"
  read -r ignored <&9
  exit 19
fi
/bin/sh -c "$3" "$nonce-child" "$nonce" child &
child=$!
printf 'READY %s parent %s %s\n' "$nonce" "$$" "$(start_time $$)"
if [ "$mode" = stdin ]; then
  read -r ignored
  exit 20
fi
wait "$child"
`

type microsandboxLogCancelFixture struct {
	framework *framework.Framework
	workspace string
	sandbox   string
	createdAt string
	bootID    string
	nonce     string
	uid       string
	label     string
	runtime   driver.ArgvExecDriver
}

var _ = ginkgo.Describe("microsandbox logs and cancellation parity",
	ginkgo.Label("up-provider-microsandbox-logs-cancel"), func() {
		for _, selection := range []struct{ name, source, runtime string }{
			{"builtin-logs-cancel", microsandboxResourceBuiltin, provider.MicrosandboxDriver},
			{"external-logs-cancel", microsandboxExternalProvider, provider.ExternalDriver},
		} {
			ginkgo.Context(selection.name, func() {
				var fixture *microsandboxLogCancelFixture
				ginkgo.BeforeEach(func(ctx context.Context) {
					fixture = newMicrosandboxLogCancelFixture(ctx, selection.name, selection.source)
					fixture.loadRuntime(ctx, selection.runtime)
				})
				ginkgo.It(
					"returns actual stdout and stderr workload logs while running and stopped",
					func(ctx context.Context) { fixture.assertLogs(ctx) },
					ginkgo.SpecTimeout(framework.TimeoutLong()),
				)
				ginkgo.It("cancels a ready Exec parent and child without replacing the VM",
					func(ctx context.Context) { fixture.assertCancellation(ctx, false) },
					ginkgo.SpecTimeout(framework.TimeoutLong()),
				)
				ginkgo.It("cancels a ready Exec with blocked owned stdin without replacing the VM",
					func(ctx context.Context) { fixture.assertCancellation(ctx, true) },
					ginkgo.SpecTimeout(framework.TimeoutLong()),
				)
			})
		}
	})

func newMicrosandboxLogCancelFixture(
	ctx context.Context, name, source string,
) *microsandboxLogCancelFixture {
	skipIfNoMicrosandbox(ctx)
	ginkgo.GinkgoT().Setenv("DEVSY_HOME", ginkgo.GinkgoT().TempDir())
	ginkgo.GinkgoT().Setenv("DEVSY_CONFIG", "")
	initialDir, err := os.Getwd()
	framework.ExpectNoError(err)
	f := framework.NewDefaultFramework(filepath.Join(initialDir, "bin"))
	workspace, err := framework.CreateTempDir()
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, workspace)
	var random [16]byte
	_, err = rand.Read(random[:])
	framework.ExpectNoError(err)
	fixture := &microsandboxLogCancelFixture{
		framework: f, workspace: workspace,
		nonce: "exec-parity-" + hex.EncodeToString(random[:]),
		uid:   encoding.CreateNewUID("", filepath.Base(workspace)),
	}
	fixture.sandbox = "devsy-" + fixture.uid
	fixture.label = "devsy.logs-cancel-parity=" + fixture.uid
	assertMicrosandboxEphemeralAbsent(ctx, fixture.label)
	out, err := microsandboxResourceCommand(ctx, "list", "--quiet")
	framework.ExpectNoError(err)
	gomega.Expect(strings.Fields(string(out))).NotTo(gomega.ContainElement(fixture.sandbox))
	framework.ExpectNoError(f.DevsyProviderAdd(ctx, source, "--name", name))
	fixture.registerCleanup(name)
	framework.ExpectNoError(f.DevsyProviderUse(ctx, name))
	writeMicrosandboxResourceConfig(workspace, nil)
	framework.ExpectNoError(os.WriteFile(
		filepath.Join(workspace, microsandboxCancelSentinel), []byte(fixture.nonce), 0o600,
	))
	fixture.up(ctx)
	gomega.Expect(microsandboxResourceSandbox(ctx, f, workspace)).To(gomega.Equal(fixture.sandbox))
	fixture.createdAt = microsandboxCreationTime(ctx, fixture.sandbox)
	fixture.bootID = microsandboxStorageBootID(ctx, f, workspace)
	return fixture
}

func (f *microsandboxLogCancelFixture) registerCleanup(name string) {
	ginkgo.DeferCleanup(func() error {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		return f.framework.DevsyProviderDelete(cleanup, name)
	})
	// Registered only after proving the fresh UID/name/label absent. A failed
	// startup can remove its Devsy record before deleting the owned backend VM.
	ginkgo.DeferCleanup(func() error {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		out, err := microsandboxResourceCommand(cleanup, "list", "--quiet")
		if err != nil {
			return err
		}
		for sandbox := range strings.FieldsSeq(string(out)) {
			if sandbox != f.sandbox {
				continue
			}
			out, err = microsandboxResourceCommand(cleanup, "remove", "--force", sandbox)
			if err != nil {
				return fmt.Errorf("remove exact test-owned sandbox %s: %w: %s", sandbox, err, out)
			}
		}
		return nil
	})
	ginkgo.DeferCleanup(f.framework.CleanupWorkspace, f.workspace)
}

func (f *microsandboxLogCancelFixture) up(ctx context.Context) {
	framework.ExpectNoError(f.framework.DevsyUp(ctx, f.workspace,
		"--uid", f.uid, "--id-label", f.label, "--devcontainer", ".devcontainer.json"))
}

func (f *microsandboxLogCancelFixture) loadRuntime(ctx context.Context, expected string) {
	workspace, err := f.framework.FindWorkspace(ctx, f.workspace)
	framework.ExpectNoError(err)
	origin, err := provider.GetWorkspaceAgentDir(pkgconfig.DefaultContext, workspace.ID)
	framework.ExpectNoError(err)
	info, err := agent.ParseAgentWorkspaceInfo(filepath.Join(origin, provider.WorkspaceConfigFile))
	framework.ExpectNoError(err)
	gomega.Expect(info.Workspace).NotTo(gomega.BeNil())
	gomega.Expect(string(info.Agent.Local)).To(gomega.Equal("true"))
	gomega.Expect(info.Agent.Driver).To(gomega.Equal(expected))
	gomega.Expect(info.Origin).To(gomega.Equal(origin))
	gomega.Expect(info.Workspace.UID).To(gomega.Equal(workspace.UID))
	gomega.Expect(info.Workspace.UID).To(gomega.Equal(f.uid))
	gomega.Expect(info.Workspace.UID).NotTo(gomega.BeEmpty())
	if expected == provider.ExternalDriver {
		binaries, resolveErr := agent.GetAgentBinariesDirFromWorkspaceDir(origin)
		framework.ExpectNoError(resolveErr)
		_, resolveErr = provider.ResolveExternalRuntimeBinary(info.Agent, binaries)
		framework.ExpectNoError(resolveErr)
	}
	bundle, err := drivercreate.New(ctx, info)
	framework.ExpectNoError(err)
	var ok bool
	f.runtime, ok = bundle.Runtime.(driver.ArgvExecDriver)
	gomega.Expect(ok).To(gomega.BeTrue(), "selected runtime must implement literal argv Exec")
	f.uid = info.Workspace.UID
}

func (f *microsandboxLogCancelFixture) assertLogs(ctx context.Context) {
	outMarker, errMarker := f.nonce+"-stdout", f.nonce+"-stderr"
	command, cancel := context.WithTimeout(ctx, 30*time.Second)
	err := f.runtime.CommandContainerArgv(command, f.uid,
		[]string{
			microsandboxCancelShell, "-c", "printf '%s\\n' \"$1\"; printf '%s\\n' \"$2\" >&2",
			f.nonce, outMarker, errMarker,
		},
		driver.Streams{Stdout: io.Discard, Stderr: io.Discard})
	cancel()
	framework.ExpectNoError(err)
	f.assertLogMarkers(ctx, outMarker, errMarker)
	f.assertUnchanged(ctx)
	framework.ExpectNoError(f.framework.DevsyStop(ctx, f.workspace))
	assertMicrosandboxEphemeralStopped(ctx, f.sandbox, f.createdAt)
	f.assertLogMarkers(ctx, outMarker, errMarker)
	assertMicrosandboxEphemeralStopped(ctx, f.sandbox, f.createdAt)
	f.up(ctx)
	gomega.Expect(microsandboxCreationTime(ctx, f.sandbox)).To(gomega.Equal(f.createdAt))
	gomega.Expect(microsandboxStorageBootID(ctx, f.framework, f.workspace)).
		NotTo(gomega.Equal(f.bootID))
	f.assertSourceAndCommand(ctx)
}

func (f *microsandboxLogCancelFixture) assertLogMarkers(ctx context.Context, markers ...string) {
	logsCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := f.framework.DevsyLogs(logsCtx, f.workspace)
	framework.ExpectNoError(err)
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	for _, marker := range markers {
		gomega.Expect(lines).To(gomega.ContainElement(marker))
	}
}

type microsandboxExecIdentity struct {
	Role  string
	PID   int
	Start string
}

type microsandboxExecReadiness struct {
	mu      sync.Mutex
	pending string
	nonce   string
	frames  chan microsandboxExecIdentity
	seen    []microsandboxExecIdentity
}

func (r *microsandboxExecReadiness) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending += string(p)
	if len(r.pending) > 4096 {
		return 0, fmt.Errorf("unexpectedly large Exec readiness output")
	}
	for {
		line, rest, found := strings.Cut(r.pending, "\n")
		if !found {
			return len(p), nil
		}
		r.pending = rest
		identity, err := parseMicrosandboxExecIdentity(line, r.nonce)
		if err != nil {
			return 0, err
		}
		r.seen = append(r.seen, identity)
		select {
		case r.frames <- identity:
		default:
			return 0, fmt.Errorf("too many Exec readiness frames")
		}
	}
}

func parseMicrosandboxExecIdentity(line, nonce string) (microsandboxExecIdentity, error) {
	fields := strings.Fields(line)
	if len(fields) != 5 || fields[0] != "READY" || fields[1] != nonce {
		return microsandboxExecIdentity{}, fmt.Errorf("unexpected Exec readiness frame %q", line)
	}
	pid, err := strconv.Atoi(fields[3])
	if err != nil || pid <= 1 {
		return microsandboxExecIdentity{}, fmt.Errorf("invalid Exec PID in %q", line)
	}
	if _, err = strconv.ParseUint(fields[4], 10, 64); err != nil {
		return microsandboxExecIdentity{}, fmt.Errorf("invalid Exec start time in %q", line)
	}
	return microsandboxExecIdentity{Role: fields[2], PID: pid, Start: fields[4]}, nil
}

func (r *microsandboxExecReadiness) snapshot() []microsandboxExecIdentity {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]microsandboxExecIdentity(nil), r.seen...)
}

type microsandboxOwnedInput struct {
	*io.PipeReader
	entered   chan struct{}
	closed    chan struct{}
	readOnce  sync.Once
	closeOnce sync.Once
}

func (r *microsandboxOwnedInput) Read(p []byte) (int, error) {
	r.readOnce.Do(func() { close(r.entered) })
	return r.PipeReader.Read(p)
}

func (r *microsandboxOwnedInput) Close() error {
	err := r.PipeReader.Close()
	r.closeOnce.Do(func() { close(r.closed) })
	return err
}

type microsandboxExecOperation struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error
	input  *microsandboxOwnedInput
	writer *io.PipeWriter
}

func (f *microsandboxLogCancelFixture) startExec(
	ctx context.Context, blocked bool, readiness *microsandboxExecReadiness,
) *microsandboxExecOperation {
	commandCtx, cancel := context.WithCancel(ctx)
	op := &microsandboxExecOperation{cancel: cancel, done: make(chan struct{})}
	mode := "wait"
	var input io.Reader
	if blocked {
		reader, writer := io.Pipe()
		op.input = &microsandboxOwnedInput{
			PipeReader: reader, entered: make(chan struct{}), closed: make(chan struct{}),
		}
		op.writer, input, mode = writer, op.input, "stdin"
	}
	ginkgo.DeferCleanup(op.cleanup)
	go func() {
		defer close(op.done)
		op.err = f.runtime.CommandContainerArgv(commandCtx, f.uid,
			[]string{
				microsandboxCancelShell, "-c", microsandboxCancelScript, f.nonce,
				f.nonce, mode, microsandboxCancelScript,
			},
			driver.Streams{Stdin: input, Stdout: readiness, Stderr: io.Discard})
	}()
	return op
}

func (op *microsandboxExecOperation) cleanup() {
	op.cancel()
	if op.input != nil {
		_ = op.input.Close()
		_ = op.writer.Close()
	}
	select {
	case <-op.done:
	case <-time.After(microsandboxCancelBound):
		ginkgo.Fail("owned Exec goroutine failed to join after cancellation and input cleanup")
	}
}

func (f *microsandboxLogCancelFixture) assertCancellation(ctx context.Context, blocked bool) {
	ready := &microsandboxExecReadiness{
		nonce:  f.nonce,
		frames: make(chan microsandboxExecIdentity, 2),
	}
	op := f.startExec(ctx, blocked, ready)
	ginkgo.DeferCleanup(func() { f.cleanupProcesses(ready.snapshot()) })
	identities := f.awaitReadiness(ctx, ready, op)
	observer, cancelObserver := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	alive, err := f.processesAlive(observer, identities)
	cancelObserver()
	framework.ExpectNoError(err)
	gomega.Expect(alive).
		To(gomega.Equal(len(identities)), "both exact identities must be live before cancellation")

	// Both bounds start at the cancellation event, not after waiting for caller exit.
	deathCtx, cancelDeath := context.WithTimeout(
		context.WithoutCancel(ctx),
		microsandboxCancelBound,
	)
	defer cancelDeath()
	op.cancel()
	select {
	case <-op.done:
		gomega.Expect(op.err).
			To(gomega.HaveOccurred(), "cancellation must not look like successful completion")
	case <-deathCtx.Done():
		ginkgo.Fail("ready Runtime Exec did not return within 10s of cancellation")
	}
	if blocked {
		select {
		case <-op.input.closed:
		default:
			ginkgo.Fail("Runtime Exec returned without closing its blocked owned input")
		}
		_, err = op.writer.Write([]byte("late producer"))
		gomega.Expect(err).To(gomega.MatchError(io.ErrClosedPipe))
	}
	f.awaitProcessDeath(deathCtx, identities)
	f.assertUnchanged(ctx)
}

func (f *microsandboxLogCancelFixture) awaitReadiness(
	ctx context.Context, ready *microsandboxExecReadiness, op *microsandboxExecOperation,
) []microsandboxExecIdentity {
	readinessCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	identities := make([]microsandboxExecIdentity, 0, 2)
	roles := make(map[string]bool)
	for len(identities) < 2 {
		select {
		case identity := <-ready.frames:
			gomega.Expect(identity.Role).To(gomega.BeElementOf("parent", "child"))
			gomega.Expect(roles[identity.Role]).To(gomega.BeFalse(), "duplicate readiness role")
			roles[identity.Role] = true
			identities = append(identities, identity)
		case <-op.done:
			ginkgo.Fail(fmt.Sprintf("Exec exited before readiness: %v", op.err))
		case <-readinessCtx.Done():
			ginkgo.Fail("guest parent and child were not ready within 30s")
		}
	}
	gomega.Expect(identities[0].PID).NotTo(gomega.Equal(identities[1].PID))
	op.awaitInputRead(readinessCtx)
	return identities
}

func (op *microsandboxExecOperation) awaitInputRead(ctx context.Context) {
	if op.input == nil {
		return
	}
	select {
	case <-op.input.entered:
	case <-op.done:
		ginkgo.Fail(fmt.Sprintf("Exec exited before stdin Read entered: %v", op.err))
	case <-ctx.Done():
		ginkgo.Fail("host input Read was not entered within the readiness bound")
	}
}

// The observer is a separate SSH operation, never the canceled Exec session.
// Identity replacement or a zombie means the original process is not live;
// unexpected procfs or SSH errors fail, rather than being interpreted as death.
func (f *microsandboxLogCancelFixture) processesAlive(
	ctx context.Context, identities []microsandboxExecIdentity,
) (int, error) {
	out, err := f.framework.DevsySSHOnce(ctx, f.workspace, f.identityCommand(identities, false))
	if err != nil {
		return 0, err
	}
	count, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("invalid process observer response %q: %w", out, err)
	}
	if count < 0 || count > len(identities) {
		return 0, fmt.Errorf("unexpected live process count %d", count)
	}
	return count, nil
}

func (f *microsandboxLogCancelFixture) identityCommand(
	identities []microsandboxExecIdentity, cleanup bool,
) string {
	var command strings.Builder
	command.WriteString("set -eu; alive=0; ")
	for _, identity := range identities {
		fmt.Fprintf(
			&command,
			"p=%d; expected=%s; ",
			identity.PID,
			shellescape.Quote(identity.Start),
		)
		command.WriteString(`if [ -d "/proc/$p" ]; then ` +
			`if stat=$(cat "/proc/$p/stat"); then ` +
			`stat=${stat##*) }; set -- $stat; state=$1; shift 19; actual=$1; ` +
			`if [ "$actual" = "$expected" ] && [ "$state" != Z ] && [ "$state" != X ]; then ` +
			`if cmdline=$(tr '\000' ' ' < "/proc/$p/cmdline"); then `)
		fmt.Fprintf(&command, "case \"$cmdline\" in *%s*) ", shellescape.Quote(f.nonce))
		if cleanup {
			command.WriteString(`kill -KILL "$p" || { [ ! -d "/proc/$p" ] || exit 31; }; `)
		} else {
			command.WriteString("alive=$((alive + 1)); ")
		}
		if cleanup {
			command.WriteString(";; *) : ;; esac; ")
		} else {
			command.WriteString(`;; *)
if latest=$(cat "/proc/$p/stat"); then
  latest=${latest##*) }; set -- $latest; latest_state=$1; shift 19
  if [ "$1" = "$expected" ] && [ "$latest_state" != Z ] && [ "$latest_state" != X ]; then exit 34; fi
else [ ! -d "/proc/$p" ] || exit 35; fi
;; esac; `)
		}
		command.WriteString(`else [ ! -d "/proc/$p" ] || exit 32; fi; fi; ` +
			`else [ ! -d "/proc/$p" ] || exit 33; fi; fi; `)
	}
	command.WriteString("printf '%s\\n' \"$alive\"")
	return command.String()
}

func (f *microsandboxLogCancelFixture) awaitProcessDeath(
	ctx context.Context, identities []microsandboxExecIdentity,
) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		alive, err := f.processesAlive(ctx, identities)
		framework.ExpectNoError(err)
		if alive == 0 {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			ginkgo.Fail(
				"original guest process identities remained live after the 10s cancellation bound",
			)
		}
	}
}

func (f *microsandboxLogCancelFixture) cleanupProcesses(identities []microsandboxExecIdentity) {
	if len(identities) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), microsandboxCancelBound)
	defer cancel()
	_, err := f.framework.DevsySSHOnce(ctx, f.workspace, f.identityCommand(identities, true))
	framework.ExpectNoError(err)
}

func (f *microsandboxLogCancelFixture) assertUnchanged(ctx context.Context) {
	out, err := microsandboxResourceCommand(
		ctx,
		"inspect",
		f.sandbox,
		"--format",
		microsandboxResourceJSON,
	)
	framework.ExpectNoError(err)
	var state microsandboxLogCancelState
	framework.ExpectNoError(json.Unmarshal(out, &state))
	gomega.Expect(state.Status).To(gomega.Equal("Running"))
	gomega.Expect(microsandboxCreationTime(ctx, f.sandbox)).To(gomega.Equal(f.createdAt))
	gomega.Expect(microsandboxStorageBootID(ctx, f.framework, f.workspace)).
		To(gomega.Equal(f.bootID))
	f.assertSourceAndCommand(ctx)
}

func (f *microsandboxLogCancelFixture) assertSourceAndCommand(ctx context.Context) {
	command, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out strings.Builder
	err := f.runtime.CommandContainerArgv(
		command,
		f.uid,
		[]string{
			microsandboxCancelShell,
			"-c",
			"printf '%s\\n' \"$1\"",
			f.nonce,
			f.nonce + "-next",
		},
		driver.Streams{Stdout: &out, Stderr: io.Discard},
	)
	framework.ExpectNoError(err)
	gomega.Expect(out.String()).To(gomega.Equal(f.nonce + "-next\n"))
	data, err := os.ReadFile(filepath.Join(f.workspace, microsandboxCancelSentinel))
	framework.ExpectNoError(err)
	gomega.Expect(string(data)).To(gomega.Equal(f.nonce))
	guestPath := filepath.Join(
		"/workspaces",
		filepath.Base(f.workspace),
		microsandboxCancelSentinel,
	)
	out.Reset()
	err = f.runtime.CommandContainerArgv(command, f.uid, []string{"cat", guestPath},
		driver.Streams{Stdout: &out, Stderr: io.Discard})
	framework.ExpectNoError(err)
	gomega.Expect(out.String()).To(gomega.Equal(f.nonce))
}
