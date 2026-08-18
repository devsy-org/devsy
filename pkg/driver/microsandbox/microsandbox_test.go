package microsandbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
)

const (
	wsName      = "devsy-ws1"
	testImage   = "example:latest"
	testUser    = "vscode"
	imgX        = "x:1"
	testImg     = "img:1"
	shPath      = "/bin/sh"
	callFind    = "find:" + wsName
	callRemove  = "remove:" + wsName
	testBindSrc = "/host/proj"
	testBindDst = "/workspaces/proj"
)

// fakeClient is an in-memory sandboxClient that records calls, so the driver's
// lifecycle logic can be tested without a live runtime.
type fakeClient struct {
	created     map[string]sandboxSpec
	info        map[string]*sandboxInfo
	calls       []string
	execReq     execRequest
	execName    string
	failFind    error
	failStop    error
	failCreat   error
	failEnsure  error
	failInstall error
}

func newFakeClient() *fakeClient {
	return &fakeClient{created: map[string]sandboxSpec{}, info: map[string]*sandboxInfo{}}
}

func (f *fakeClient) EnsureInstalled(context.Context) error { return f.failInstall }

func (f *fakeClient) EnsureImage(_ context.Context, image string) error {
	f.calls = append(f.calls, "ensure:"+image)
	return f.failEnsure
}

func (f *fakeClient) Create(_ context.Context, name string, spec sandboxSpec) error {
	f.calls = append(f.calls, "create:"+name)
	if f.failCreat != nil {
		return f.failCreat
	}
	f.created[name] = spec
	f.info[name] = &sandboxInfo{
		Name:      name,
		Running:   true,
		CreatedAt: time.Unix(0, 0),
		Labels:    spec.Labels,
	}
	return nil
}

func (f *fakeClient) Find(_ context.Context, name string) (*sandboxInfo, error) {
	f.calls = append(f.calls, "find:"+name)
	if f.failFind != nil {
		return nil, f.failFind
	}
	return f.info[name], nil
}

func (f *fakeClient) Start(_ context.Context, name string) error {
	f.calls = append(f.calls, "start:"+name)
	return nil
}

func (f *fakeClient) Stop(_ context.Context, name string) error {
	f.calls = append(f.calls, "stop:"+name)
	if f.failStop != nil {
		return f.failStop
	}
	if info := f.info[name]; info != nil {
		info.Running = false
	}
	return nil
}

func (f *fakeClient) Remove(_ context.Context, name string) error {
	f.calls = append(f.calls, "remove:"+name)
	delete(f.info, name)
	return nil
}

func (f *fakeClient) Exec(_ context.Context, name string, req execRequest) error {
	f.calls = append(f.calls, "exec:"+name)
	f.execName = name
	f.execReq = req
	return nil
}

func (f *fakeClient) Logs(_ context.Context, name string, _ io.Writer) error {
	f.calls = append(f.calls, "logs:"+name)
	return nil
}

var _ sandboxClient = (*fakeClient)(nil)

func TestRunDevContainerBuildsSpec(t *testing.T) {
	f := newFakeClient()
	d := newDriver(f, nil, specDefaults{memory: 2048, cpus: 4, ephemeral: true})

	err := d.RunDevContainer(context.Background(), "ws1", &driver.RunOptions{
		Image: testImage,
		User:  testUser,
		Env:   map[string]string{"FOO": "bar"},
	})
	if err != nil {
		t.Fatalf("RunDevContainer: %v", err)
	}

	spec, ok := f.created[wsName]
	if !ok {
		t.Fatal("expected a sandbox named devsy-ws1 to be created")
	}
	got := struct {
		image     string
		memory    uint32
		cpus      uint8
		ephemeral bool
	}{spec.Image, spec.Memory, spec.CPUs, spec.Ephemeral}
	want := struct {
		image     string
		memory    uint32
		cpus      uint8
		ephemeral bool
	}{testImage, 2048, 4, true}
	if got != want {
		t.Errorf("spec = %+v, want %+v", got, want)
	}
	if spec.Labels[userLabel] != testUser {
		t.Errorf("user label = %q, want vscode", spec.Labels[userLabel])
	}
	if spec.Env["FOO"] != "bar" {
		t.Errorf("env not propagated: %+v", spec.Env)
	}
}

func TestRunDevContainerReplacesStaleSandbox(t *testing.T) {
	f := newFakeClient()
	f.info[wsName] = &sandboxInfo{Name: wsName, Running: true}
	d := newDriver(f, nil, specDefaults{})

	err := d.RunDevContainer(context.Background(), "ws1", &driver.RunOptions{Image: imgX})
	if err != nil {
		t.Fatalf("RunDevContainer: %v", err)
	}
	want := []string{
		callFind,
		"stop:devsy-ws1",
		callRemove,
		"ensure:x:1",
		"create:devsy-ws1",
	}
	if !slices.Equal(f.calls, want) {
		t.Errorf("call order = %v, want %v", f.calls, want)
	}
}

func TestRunDevContainerContinuesWhenPrePullFails(t *testing.T) {
	f := newFakeClient()
	f.failEnsure = errors.New("registry hiccup")
	d := newDriver(f, nil, specDefaults{})

	// Pre-pull failure must not abort the run; create should still be attempted.
	if err := d.RunDevContainer(
		context.Background(),
		"ws1",
		&driver.RunOptions{Image: imgX},
	); err != nil {
		t.Fatalf("RunDevContainer should proceed despite pull failure: %v", err)
	}
	if _, ok := f.created[wsName]; !ok {
		t.Error("expected create to run even though pre-pull failed")
	}
}

func TestRunImageDevContainerRunsFromParams(t *testing.T) {
	f := newFakeClient()
	d := newDriver(f, nil, specDefaults{})

	err := d.RunImageDevContainer(context.Background(), &driver.RunImageDevContainerParams{
		WorkspaceID: "ws1",
		Options:     &driver.RunOptions{Image: "built-local:latest"},
	})
	if err != nil {
		t.Fatalf("RunImageDevContainer: %v", err)
	}
	want := []string{callFind, "ensure:built-local:latest", "create:devsy-ws1"}
	if !slices.Equal(f.calls, want) {
		t.Errorf("call order = %v, want %v", f.calls, want)
	}
}

func TestCheckGPURequirement(t *testing.T) {
	// required GPU -> error
	req := &config.DevContainerConfig{}
	req.HostRequirements = &config.HostRequirements{GPU: &config.GPURequirement{Value: "true"}}
	if err := checkGPURequirement(req); err == nil {
		t.Error("expected an error when a GPU is required")
	}
	// optional GPU -> no error
	opt := &config.DevContainerConfig{}
	opt.HostRequirements = &config.HostRequirements{GPU: &config.GPURequirement{Value: "optional"}}
	if err := checkGPURequirement(opt); err != nil {
		t.Errorf("optional GPU should not error, got %v", err)
	}
	// no GPU / nil config -> no error
	if err := checkGPURequirement(nil); err != nil {
		t.Errorf("nil config should not error, got %v", err)
	}
	if err := checkGPURequirement(&config.DevContainerConfig{}); err != nil {
		t.Errorf("no host requirements should not error, got %v", err)
	}
}

func TestUpdateContainerUserUIDIsNoop(t *testing.T) {
	d := newDriver(newFakeClient(), nil, specDefaults{})
	if err := d.UpdateContainerUserUID(context.Background(), "ws1", nil, nil); err != nil {
		t.Errorf("UpdateContainerUserUID should be a no-op, got %v", err)
	}
}

func TestRunDevContainerRequiresImage(t *testing.T) {
	d := newDriver(newFakeClient(), nil, specDefaults{})
	if err := d.RunDevContainer(context.Background(), "ws1", &driver.RunOptions{}); err == nil {
		t.Fatal("expected an error when image is empty")
	}
}

func TestFindDevContainerMapsState(t *testing.T) {
	f := newFakeClient()
	f.info[wsName] = &sandboxInfo{
		Name:      wsName,
		Running:   true,
		CreatedAt: time.Unix(0, 0),
		Labels:    map[string]string{userLabel: testUser},
	}
	d := newDriver(f, nil, specDefaults{})

	details, err := d.FindDevContainer(context.Background(), "ws1")
	if err != nil {
		t.Fatalf("FindDevContainer: %v", err)
	}
	if details == nil {
		t.Fatal("expected details for an existing sandbox")
	}
	if details.State.Status != "running" {
		t.Errorf("status = %q, want running", details.State.Status)
	}
	if details.Config.User != testUser {
		t.Errorf("user = %q, want vscode", details.Config.User)
	}
}

func TestFindDevContainerAbsent(t *testing.T) {
	d := newDriver(newFakeClient(), nil, specDefaults{})
	details, err := d.FindDevContainer(context.Background(), "missing")
	if err != nil {
		t.Fatalf("FindDevContainer: %v", err)
	}
	if details != nil {
		t.Errorf("expected nil details for a missing sandbox, got %+v", details)
	}
}

func TestDeleteStopsRunningThenRemoves(t *testing.T) {
	f := newFakeClient()
	f.info[wsName] = &sandboxInfo{Name: wsName, Running: true}
	d := newDriver(f, nil, specDefaults{})

	if err := d.DeleteDevContainer(context.Background(), "ws1"); err != nil {
		t.Fatalf("DeleteDevContainer: %v", err)
	}
	want := []string{callFind, "stop:devsy-ws1", callRemove}
	if !slices.Equal(f.calls, want) {
		t.Errorf("call order = %v, want %v", f.calls, want)
	}
}

func TestDeleteStoppedSkipsStop(t *testing.T) {
	f := newFakeClient()
	f.info[wsName] = &sandboxInfo{Name: wsName, Running: false}
	d := newDriver(f, nil, specDefaults{})

	if err := d.DeleteDevContainer(context.Background(), "ws1"); err != nil {
		t.Fatalf("DeleteDevContainer: %v", err)
	}
	want := []string{callFind, callRemove}
	if !slices.Equal(f.calls, want) {
		t.Errorf("call order = %v, want %v", f.calls, want)
	}
}

func TestDeleteAbsentIsNoop(t *testing.T) {
	f := newFakeClient()
	d := newDriver(f, nil, specDefaults{})
	if err := d.DeleteDevContainer(context.Background(), "missing"); err != nil {
		t.Fatalf("DeleteDevContainer: %v", err)
	}
	want := []string{"find:devsy-missing"}
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want just a find", f.calls)
	}
}

func TestDeletePropagatesStopError(t *testing.T) {
	f := newFakeClient()
	f.info[wsName] = &sandboxInfo{Name: wsName, Running: true}
	f.failStop = errors.New("boom")
	d := newDriver(f, nil, specDefaults{})

	if err := d.DeleteDevContainer(context.Background(), "ws1"); err == nil {
		t.Fatal("expected the stop error to propagate")
	}
}

func TestCommandDevContainerForwardsRequest(t *testing.T) {
	f := newFakeClient()
	d := newDriver(f, nil, specDefaults{})
	var out bytes.Buffer

	err := d.CommandDevContainer(context.Background(), &driver.CommandParams{
		WorkspaceID: "ws1",
		User:        testUser,
		Command:     "echo hi",
		Stdout:      &out,
	})
	if err != nil {
		t.Fatalf("CommandDevContainer: %v", err)
	}
	if f.execName != wsName {
		t.Errorf("exec name = %q, want devsy-ws1", f.execName)
	}
	if f.execReq.Command != "echo hi" || f.execReq.User != testUser {
		t.Errorf("unexpected exec request: %+v", f.execReq)
	}
}

func TestRunDevContainerSetsEntrypoint(t *testing.T) {
	f := newFakeClient()
	d := newDriver(f, nil, specDefaults{})

	err := d.RunDevContainer(context.Background(), "ws1", &driver.RunOptions{
		Image:      testImage,
		Entrypoint: shPath,
		Cmd:        []string{"-c", "start", "-"},
	})
	if err != nil {
		t.Fatalf("RunDevContainer: %v", err)
	}
	got := f.created[wsName]
	if got.Entrypoint != shPath {
		t.Errorf("entrypoint = %q, want %q", got.Entrypoint, shPath)
	}
	want := []string{"-c", "start", "-"}
	if !slices.Equal(got.Cmd, want) {
		t.Errorf("cmd = %v, want %v", got.Cmd, want)
	}
}

func TestCommandContainerArgvForwardsArgv(t *testing.T) {
	f := newFakeClient()
	d := newDriver(f, nil, specDefaults{})

	argv := []string{"sh", "-c", "cat > /usr/local/bin/devsy"}
	err := d.CommandContainerArgv(context.Background(), "ws1", argv, driver.Streams{})
	if err != nil {
		t.Fatalf("CommandContainerArgv: %v", err)
	}
	if f.execName != wsName {
		t.Errorf("exec name = %q, want devsy-ws1", f.execName)
	}
	if len(f.execReq.Argv) != len(argv) || f.execReq.Argv[0] != "sh" {
		t.Errorf("argv = %v, want %v", f.execReq.Argv, argv)
	}
	if f.execReq.Command != "" {
		t.Errorf("argv exec must not set a shell command, got %q", f.execReq.Command)
	}
	if f.execReq.User != "root" {
		t.Errorf("argv exec user = %q, want root", f.execReq.User)
	}
}

func TestSandboxName(t *testing.T) {
	if got := sandboxName("my-workspace"); got != "devsy-my-workspace" {
		t.Errorf("sandboxName = %q, want %q", got, "devsy-my-workspace")
	}
}

func TestSandboxNameClampsLongIDs(t *testing.T) {
	long := strings.Repeat("a", 200)
	got := sandboxName(long)
	if len(got) != maxSandboxNameLen {
		t.Errorf("clamped name length = %d, want %d", len(got), maxSandboxNameLen)
	}
	// Distinct long ids must not collide after clamping.
	other := sandboxName(strings.Repeat("a", 199) + "b")
	if got == other {
		t.Error("distinct long ids produced the same clamped name")
	}
	// Short ids stay verbatim.
	if sandboxName("x") != "devsy-x" {
		t.Errorf("short id should not be clamped, got %q", sandboxName("x"))
	}
}

func TestParseUint32(t *testing.T) {
	cases := []struct {
		in   string
		want uint32
	}{{"", 0}, {"  ", 0}, {"2048", 2048}, {"not-num", 0}, {"-5", 0}}
	for _, c := range cases {
		if got := parseUint32(c.in); got != c.want {
			t.Errorf("parseUint32(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"":      0,
		"  ":    0,
		"20s":   20 * time.Second,
		"1h":    time.Hour,
		"bogus": 0,
		"-5m":   0,
	}
	for in, want := range cases {
		if got := parseDuration(in); got != want {
			t.Errorf("parseDuration(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestBuildSpecMapsAllMountTypes(t *testing.T) {
	d := newDriver(newFakeClient(), nil, specDefaults{})
	spec := d.buildSpec("ws1", &driver.RunOptions{
		Image: imgX,
		Mounts: []*config.Mount{
			{Type: driver.MountTypeVolume, Source: "vol1", Target: "/data"},
			{Type: driver.MountTypeTmpfs, Target: "/scratch"},
			{Type: driver.MountTypeBind, Source: testBindSrc, Target: "/mnt"},
			nil,
		},
	})
	want := []volumeMount{
		{Target: "/data", Volume: "vol1"},
		{Target: "/scratch", Tmpfs: true},
		{Target: "/mnt", Source: testBindSrc},
	}
	if !slices.Equal(spec.Mounts, want) {
		t.Errorf("mounts = %+v, want %+v", spec.Mounts, want)
	}
}

func TestBuildSpecMapsWorkspaceMount(t *testing.T) {
	d := newDriver(newFakeClient(), nil, specDefaults{})
	spec := d.buildSpec("ws1", &driver.RunOptions{
		Image: imgX,
		WorkspaceMount: &config.Mount{
			Type:   driver.MountTypeBind,
			Source: testBindSrc,
			Target: testBindDst,
		},
	})
	want := []volumeMount{{Target: testBindDst, Source: testBindSrc}}
	if !slices.Equal(spec.Mounts, want) {
		t.Errorf("mounts = %+v, want %+v", spec.Mounts, want)
	}
}

func TestBuildSpecCarriesIdleTimeout(t *testing.T) {
	d := newDriver(newFakeClient(), nil, specDefaults{idleTimeout: 90 * time.Second})
	spec := d.buildSpec("ws1", &driver.RunOptions{Image: imgX})
	if spec.IdleTimeout != 90*time.Second {
		t.Errorf("idle timeout = %s, want 90s", spec.IdleTimeout)
	}
}

func TestBuildSpecCarriesCeilingsAndEgress(t *testing.T) {
	d := newDriver(
		newFakeClient(),
		nil,
		specDefaults{maxMemory: 4096, maxCPUs: 4, blockEgress: true},
	)
	spec := d.buildSpec("ws1", &driver.RunOptions{Image: imgX})
	if spec.MaxMemory != 4096 || spec.MaxCPUs != 4 || !spec.BlockEgress {
		t.Errorf("unexpected spec ceilings/egress: %+v", spec)
	}
}

func TestParseUint8(t *testing.T) {
	cases := []struct {
		in   string
		want uint8
	}{{"", 0}, {"2", 2}, {"999", 0}, {"abc", 0}, {" 4 ", 4}}
	for _, c := range cases {
		if got := parseUint8(c.in); got != c.want {
			t.Errorf("parseUint8(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
