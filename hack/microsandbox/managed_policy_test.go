//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	managedPolicyTrue      = "true"
	managedPolicyParentNS  = "mnt:[parent]"
	managedPolicyChildNS   = "mnt:[child]"
	managedPolicyTestArg   = "-test.v"
	managedPolicyEnvPath   = "PATH=/bin"
	managedPolicyNotDirMsg = "RUNNER_TEMP must name an existing directory"
	managedPolicyBinaryMsg = "expected the already-built ./e2e.test binary as the first argument"
	managedPolicyAbsentMsg = "managed-policy fixture requires /etc/microsandbox to be absent in the overlay lower layer"
)

type managedPolicyFake struct {
	current        string
	overlayTarget  string
	overlayOptions string
	mountInfo      string
	replaced       bool
	path           string
	argv           []string
	env            []string
}

// newManagedPolicyFake returns a runtime whose privileged calls are recorded
// instead of performed. Ownership is the test process's own uid/gid so the
// fixture checks pass without root.
func newManagedPolicyFake(euid int, etcRoot string) (managedPolicyRuntime, *managedPolicyFake) {
	fake := &managedPolicyFake{current: managedPolicyChildNS}
	rt := managedPolicyRuntime{
		euid:    euid,
		owner:   managedPolicyOwner{uid: uint32(os.Getuid()), gid: uint32(os.Getgid())},
		etcRoot: etcRoot,
		mountNamespace: func() (string, error) {
			return fake.current, nil
		},
		mountOverlay: func(target, options string) error {
			fake.overlayTarget, fake.overlayOptions = target, options
			return nil
		},
		readMountInfo: func() (string, error) {
			if fake.mountInfo != "" {
				return fake.mountInfo, nil
			}
			return fmt.Sprintf(
				"36 25 0:32 / %s rw - overlay overlay %s",
				fake.overlayTarget,
				fake.overlayOptions,
			), nil
		},
		lookPath: func(string) (string, error) { return "/usr/bin/unshare", nil },
		executable: func() (string, error) {
			return "/abs/devsy", nil
		},
		replace: func(path string, argv, env []string) error {
			fake.replaced, fake.path, fake.argv, fake.env = true, path, argv, env
			return nil
		},
	}
	return rt, fake
}

func requireManagedPolicyMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %o, want %o", path, got, want)
	}
}

type managedPolicyInputCase struct {
	name       string
	euid       int
	marker     string
	runnerTemp string
	args       []string
	want       string
}

func managedPolicyInputCases(validTemp string) []managedPolicyInputCase {
	binary := []string{managedPolicyTestBinary}
	return []managedPolicyInputCase{
		{
			name: "non-root", euid: 1000, marker: managedPolicyTrue, runnerTemp: validTemp,
			args: binary, want: "managed-policy fixture must run as root",
		},
		{
			name: "missing marker", euid: 0, marker: "", runnerTemp: validTemp,
			args: binary, want: "managed-policy E2E wrapper requires the CI expectation marker",
		},
		{
			name: "empty runner temp", euid: 0, marker: managedPolicyTrue, runnerTemp: "",
			args: binary, want: managedPolicyNotDirMsg,
		},
		{
			name:   "runner temp absent",
			euid:   0,
			marker: managedPolicyTrue,
			runnerTemp: filepath.Join(
				validTemp,
				"absent",
			),
			args: binary,
			want: managedPolicyNotDirMsg,
		},
		{
			name: "wrong binary", euid: 0, marker: managedPolicyTrue, runnerTemp: validTemp,
			args: []string{"./other"}, want: managedPolicyBinaryMsg,
		},
		{
			name: "no arguments", euid: 0, marker: managedPolicyTrue, runnerTemp: validTemp,
			want: managedPolicyBinaryMsg,
		},
	}
}

func TestManagedPolicyE2EInputInvariants(t *testing.T) {
	for _, tc := range managedPolicyInputCases(t.TempDir()) {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(managedPolicyMarker, tc.marker)
			t.Setenv("RUNNER_TEMP", tc.runnerTemp)
			rt, fake := newManagedPolicyFake(tc.euid, t.TempDir())
			err := runManagedPolicyE2E(rt, tc.args)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("runManagedPolicyE2E() error = %v, want %q", err, tc.want)
			}
			if fake.replaced {
				t.Fatal("re-exec happened despite invalid input")
			}
		})
	}
}

func TestManagedPolicyE2EReexecsWithFixtureUnderRunnerTemp(t *testing.T) {
	runnerTemp := t.TempDir()
	t.Setenv(managedPolicyMarker, managedPolicyTrue)
	t.Setenv("RUNNER_TEMP", runnerTemp)
	rt, fake := newManagedPolicyFake(0, t.TempDir())
	fake.current = managedPolicyParentNS

	if err := runManagedPolicyE2E(
		rt,
		[]string{managedPolicyTestBinary, managedPolicyTestArg},
	); err != nil {
		t.Fatal(err)
	}
	if !fake.replaced {
		t.Fatal("runManagedPolicyE2E() did not re-exec")
	}
	if len(fake.argv) < 8 {
		t.Fatalf("argv = %#v, too short", fake.argv)
	}
	root := fake.argv[7]
	assertManagedPolicyFixtureRoot(t, root, runnerTemp)
	want := []string{
		"unshare", "--mount", "--propagation", "private", "--",
		"/abs/devsy", "managed-policy-namespace", root, filepath.Join(root, "mount-namespace-id"),
		managedPolicyParentNS, managedPolicyTestBinary, managedPolicyTestArg,
	}
	if fake.path != "/usr/bin/unshare" || !slices.Equal(fake.argv, want) {
		t.Fatalf("exec path = %q argv = %#v, want /usr/bin/unshare %#v", fake.path, fake.argv, want)
	}
}

func assertManagedPolicyFixtureRoot(t *testing.T, root, runnerTemp string) {
	t.Helper()
	if filepath.Dir(root) != runnerTemp ||
		!strings.HasPrefix(filepath.Base(root), "devsy-managed-policy.") {
		t.Fatalf(
			"fixture root %q is not a devsy-managed-policy directory under RUNNER_TEMP %q",
			root,
			runnerTemp,
		)
	}
	for _, dir := range []string{root, filepath.Join(root, "upper"), filepath.Join(root, "work")} {
		requireManagedPolicyMode(t, dir, 0o700)
	}
}

func TestCreateManagedPolicyFixtureRejectsUnexpectedOwner(t *testing.T) {
	runnerTemp := t.TempDir()
	wrongOwner := managedPolicyOwner{uid: uint32(os.Getuid()) + 1, gid: uint32(os.Getgid())}
	_, err := createManagedPolicyFixture(runnerTemp, wrongOwner)
	if err == nil ||
		!strings.HasPrefix(
			err.Error(),
			"managed-policy fixture directory has unexpected ownership or mode: ",
		) {
		t.Fatalf("createManagedPolicyFixture() error = %v, want ownership failure", err)
	}
}

type managedPolicyNamespaceCase struct {
	name  string
	euid  int
	args  []string
	setup func(t *testing.T, etc string, fake *managedPolicyFake)
	want  string
}

func mkExistingPolicyDir(t *testing.T, etc string, _ *managedPolicyFake) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(etc, "microsandbox"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func linkDanglingPolicy(t *testing.T, etc string, _ *managedPolicyFake) {
	t.Helper()
	if err := os.Symlink("/nonexistent/target", filepath.Join(etc, "microsandbox")); err != nil {
		t.Fatal(err)
	}
}

func setParentNamespace(_ *testing.T, _ string, fake *managedPolicyFake) {
	fake.current = managedPolicyParentNS
}

func setForeignOverlay(_ *testing.T, _ string, fake *managedPolicyFake) {
	fake.mountInfo = "36 25 0:32 / /etc rw - overlay overlay " +
		"lowerdir=/etc,upperdir=/elsewhere/upper,workdir=/elsewhere/work"
}

func managedPolicyNamespaceCases() []managedPolicyNamespaceCase {
	return []managedPolicyNamespaceCase{
		{name: "non-root", euid: 1000, want: "managed-policy namespace must run as root"},
		{
			name: "too few arguments",
			euid: 0,
			args: []string{"root", "id", managedPolicyParentNS},
			want: "managed-policy-namespace requires <fixture-root> <namespace-id-file> <parent-namespace> <command>",
		},
		{
			name:  "namespace unchanged",
			euid:  0,
			setup: setParentNamespace,
			want:  "mount namespace did not change",
		},
		{
			name:  "mountinfo names other directories",
			euid:  0,
			setup: setForeignOverlay,
			want:  "private /etc overlay does not use this fixture's owned upper and work directories",
		},
		{
			name:  "existing policy directory",
			euid:  0,
			setup: mkExistingPolicyDir,
			want:  managedPolicyAbsentMsg,
		},
		{
			name:  "dangling policy symlink",
			euid:  0,
			setup: linkDanglingPolicy,
			want:  managedPolicyAbsentMsg,
		},
	}
}

func TestManagedPolicyNamespaceInvariants(t *testing.T) {
	for _, tc := range managedPolicyNamespaceCases() {
		t.Run(tc.name, func(t *testing.T) {
			etc := t.TempDir()
			root := t.TempDir()
			rt, fake := newManagedPolicyFake(tc.euid, etc)
			if tc.setup != nil {
				tc.setup(t, etc, fake)
			}
			args := tc.args
			if args == nil {
				args = []string{
					root,
					filepath.Join(root, "id"),
					managedPolicyParentNS,
					managedPolicyTestBinary,
				}
			}
			err := runManagedPolicyNamespace(rt, args)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("runManagedPolicyNamespace() error = %v, want %q", err, tc.want)
			}
			if fake.replaced {
				t.Fatal("namespace stage executed the command despite failing invariant")
			}
		})
	}
}

func TestManagedPolicyNamespaceInstallsPolicyAndExecsCommand(t *testing.T) {
	etc := t.TempDir()
	root := t.TempDir()
	t.Setenv("DEVSY_TEST_MSB_FIXTURE_ROOT", "/stale")
	rt, fake := newManagedPolicyFake(0, etc)
	idFile := filepath.Join(root, "mount-namespace-id")

	args := []string{
		root,
		idFile,
		managedPolicyParentNS,
		managedPolicyTestBinary,
		managedPolicyTestArg,
	}
	if err := runManagedPolicyNamespace(rt, args); err != nil {
		t.Fatal(err)
	}
	assertManagedPolicyNamespaceID(t, idFile)
	assertManagedPolicyOverlay(t, fake, etc, root)
	assertManagedPolicyInstalled(t, etc)
	assertManagedPolicyExec(t, fake, etc, root)
}

func assertManagedPolicyNamespaceID(t *testing.T, idFile string) {
	t.Helper()
	id, err := os.ReadFile(idFile)
	if err != nil || string(id) != managedPolicyChildNS+"\n" {
		t.Fatalf("namespace id = %q, err = %v", id, err)
	}
	requireManagedPolicyMode(t, idFile, 0o600)
}

func assertManagedPolicyOverlay(t *testing.T, fake *managedPolicyFake, etc, root string) {
	t.Helper()
	wantOptions := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s",
		etc, filepath.Join(root, "upper"), filepath.Join(root, "work"))
	if fake.overlayTarget != etc || fake.overlayOptions != wantOptions {
		t.Fatalf(
			"overlay = %q %q, want %q %q",
			fake.overlayTarget,
			fake.overlayOptions,
			etc,
			wantOptions,
		)
	}
}

func assertManagedPolicyInstalled(t *testing.T, etc string) {
	t.Helper()
	policyDir := filepath.Join(etc, "microsandbox")
	policy := filepath.Join(policyDir, "managed.json")
	data, err := os.ReadFile(policy)
	if err != nil || string(data) != managedPolicyDocument {
		t.Fatalf("policy = %q, err = %v", data, err)
	}
	requireManagedPolicyMode(t, policy, 0o600)
	requireManagedPolicyMode(t, policyDir, 0o755)
	entries, err := os.ReadDir(policyDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("policy dir entries = %v, err = %v; want only managed.json", entries, err)
	}
}

func assertManagedPolicyExec(t *testing.T, fake *managedPolicyFake, etc, root string) {
	t.Helper()
	wantArgv := []string{managedPolicyTestBinary, managedPolicyTestArg}
	if !fake.replaced || fake.path != managedPolicyTestBinary ||
		!slices.Equal(fake.argv, wantArgv) {
		t.Fatalf("exec path = %q argv = %#v", fake.path, fake.argv)
	}
	if slices.Contains(fake.env, "DEVSY_TEST_MSB_FIXTURE_ROOT=/stale") {
		t.Fatal("stale DEVSY_TEST_MSB_FIXTURE_ROOT leaked into child environment")
	}
	policy := filepath.Join(etc, "microsandbox", "managed.json")
	for _, want := range []string{
		"DEVSY_REQUIRE_MSB_MANAGED_POLICY_FIXTURE=" + managedPolicyTrue,
		"DEVSY_TEST_MSB_MANAGED_POLICY=" + policy,
		"DEVSY_TEST_MSB_FIXTURE_ROOT=" + root,
		"DEVSY_TEST_MSB_EXPECTED_UPPER=" + filepath.Join(root, "upper"),
	} {
		if !slices.Contains(fake.env, want) {
			t.Errorf("child environment missing %q", want)
		}
	}
}

func TestHasOverlayMount(t *testing.T) {
	const line = "36 25 0:32 / /etc rw,relatime shared:1 - overlay overlay " +
		"rw,lowerdir=/etc,upperdir=/s/upper,workdir=/s/work"
	cases := []struct {
		name      string
		mountInfo string
		want      bool
	}{
		{name: "matching overlay", mountInfo: line, want: true},
		{
			name: "upper and work on different lines",
			mountInfo: "36 25 0:32 / /etc rw - overlay overlay upperdir=/s/upper\n" +
				"37 25 0:33 / /etc rw - overlay overlay workdir=/s/work",
			want: false,
		},
		{
			name:      "other mount point",
			mountInfo: strings.Replace(line, " /etc ", " /usr ", 1),
			want:      false,
		},
		{
			name:      "not an overlay",
			mountInfo: strings.Replace(line, "overlay overlay", "tmpfs tmpfs", 1),
			want:      false,
		},
		{
			name:      "different upper path",
			mountInfo: strings.Replace(line, "/s/upper", "/x/upper", 1),
			want:      false,
		},
		{name: "truncated line", mountInfo: "36 25 /etc", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasOverlayMount(tc.mountInfo, "/etc", "/s/upper", "/s/work"); got != tc.want {
				t.Fatalf("hasOverlayMount() = %t, want %t", got, tc.want)
			}
		})
	}
}

func countEnvPrefix(env []string, prefix string) int {
	count := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			count++
		}
	}
	return count
}

func TestManagedPolicyChildEnvironmentReplacesManagedKeys(t *testing.T) {
	environ := []string{
		managedPolicyEnvPath,
		"RUNNER_TEMP=/rt",
		"DEVSY_TEST_MSB_FIXTURE_ROOT=/stale",
		"DEVSY_TEST_MSB_EXPECTED_UPPER=/stale",
	}
	ns := managedPolicyNamespace{root: "/r"}
	env := managedPolicyChildEnvironment(environ, ns, "/etc/microsandbox/managed.json")

	for _, keep := range []string{managedPolicyEnvPath, "RUNNER_TEMP=/rt"} {
		if !slices.Contains(env, keep) {
			t.Errorf("environment dropped %q", keep)
		}
	}
	for _, stale := range []string{"DEVSY_TEST_MSB_FIXTURE_ROOT=/stale", "DEVSY_TEST_MSB_EXPECTED_UPPER=/stale"} {
		if slices.Contains(env, stale) {
			t.Errorf("environment kept stale %q", stale)
		}
	}
	if !slices.Contains(env, "DEVSY_TEST_MSB_FIXTURE_ROOT=/r") {
		t.Error("environment missing fixture root override")
	}
	if n := countEnvPrefix(env, "DEVSY_TEST_MSB_EXPECTED_UPPER="); n != 1 {
		t.Fatalf("DEVSY_TEST_MSB_EXPECTED_UPPER entries = %d, want 1", n)
	}
}
