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
	managedPolicyParentNS = "mnt:[parent]"
	managedPolicyChildNS  = "mnt:[child]"
	managedPolicyPathEnv  = "PATH=/bin"
	managedPolicyTestArg  = "-test.v"
	managedPolicyEnabled  = "true"
)

// managedPolicyFake records privileged calls instead of performing them.
type managedPolicyFake struct {
	namespace string
	mountInfo string
	options   string
	exec      string
	argv      []string
	env       []string
}

// newManagedPolicyFake returns a root-like runtime whose ownership checks match the test user.
func newManagedPolicyFake(etc string) (managedPolicyRuntime, *managedPolicyFake) {
	fake := &managedPolicyFake{namespace: managedPolicyParentNS}
	rt := managedPolicyRuntime{
		euid:           0,
		uid:            uint32(os.Getuid()),
		gid:            uint32(os.Getgid()),
		etcRoot:        etc,
		mountNamespace: func() (string, error) { return fake.namespace, nil },
		mountOverlay: func(_, options string) error {
			fake.options = options
			return nil
		},
		readMountInfo: func() (string, error) { return fake.mountInfo, nil },
		lookPath:      func(name string) (string, error) { return "/usr/bin/" + name, nil },
		executable:    func() (string, error) { return "/bin/e2e-wrapper", nil },
		replace: func(path string, argv, env []string) error {
			fake.exec, fake.argv, fake.env = path, argv, env
			return nil
		},
	}
	return rt, fake
}

func overlayMountInfo(mountPoint, upper, work string) string {
	return fmt.Sprintf(
		"36 25 0:31 / %s rw,relatime - overlay overlay rw,lowerdir=/etc,upperdir=%s,workdir=%s",
		mountPoint,
		upper,
		work,
	)
}

func requireManagedPolicyMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

// requireUnshareInvocation checks the argv that stage 1 hands to unshare.
func requireUnshareInvocation(t *testing.T, argv []string, runnerTemp string, args []string) {
	t.Helper()
	// argv: 0 unshare, 1-4 flags, 5 self, 6 namespace cmd, 7 root, 8 id file, 9 parent, 10+ args.
	if len(argv) < 10 || argv[6] != managedPolicyNamespaceCommand ||
		argv[9] != managedPolicyParentNS {
		t.Fatalf("argv = %q", argv)
	}
	if !slices.Equal(argv[10:], args) {
		t.Fatalf("forwarded args = %q, want %q", argv[10:], args)
	}
	requireFixtureRoot(t, argv[7], argv[8], runnerTemp)
}

// requireFixtureRoot checks the fixture lies under RUNNER_TEMP, the id file is inside it,
// and its directories are 0700.
func requireFixtureRoot(t *testing.T, root, idFile, runnerTemp string) {
	t.Helper()
	if filepath.Dir(root) != runnerTemp ||
		!strings.HasPrefix(filepath.Base(root), "devsy-managed-policy.") {
		t.Fatalf("fixture root %q not under RUNNER_TEMP", root)
	}
	if idFile != filepath.Join(root, managedPolicyIDFile) {
		t.Fatalf("id file = %q", idFile)
	}
	for _, dir := range []string{root, filepath.Join(root, "upper"), filepath.Join(root, "work")} {
		requireManagedPolicyMode(t, dir, 0o700)
	}
}

// requireNamespaceID checks the id file written by stage 2 holds the child namespace.
func requireNamespaceID(t *testing.T, idFile, want string) {
	t.Helper()
	id, err := os.ReadFile(idFile)
	if err != nil || string(id) != want+"\n" {
		t.Fatalf("id file = %q, %v", id, err)
	}
	requireManagedPolicyMode(t, idFile, 0o600)
}

// requireInstalledPolicy checks the initial policy file and its directory.
func requireInstalledPolicy(t *testing.T, policy string) {
	t.Helper()
	data, err := os.ReadFile(policy)
	if err != nil || string(data) != managedPolicyDocument {
		t.Fatalf("policy = %q, %v", data, err)
	}
	requireManagedPolicyMode(t, policy, 0o600)
	requireManagedPolicyMode(t, filepath.Dir(policy), 0o755)
}

func TestManagedPolicyE2EInputInvariants(t *testing.T) {
	valid := t.TempDir()
	good := []string{managedPolicyTestBinary}
	cases := []struct {
		name, marker, runnerTemp, want string
		euid                           int
		args                           []string
	}{
		{
			"non-root", managedPolicyEnabled, valid,
			"managed-policy fixture must run as root", 1000, good,
		},
		{
			"missing marker", "", valid,
			"managed-policy E2E wrapper requires the CI expectation marker", 0, good,
		},
		{
			"missing runner temp", managedPolicyEnabled, filepath.Join(valid, "absent"),
			"RUNNER_TEMP must name an existing directory", 0, good,
		},
		{
			"wrong binary", managedPolicyEnabled, valid,
			"expected the already-built ./e2e.test binary as the first argument", 0,
			[]string{"./other.test"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(managedPolicyMarker, tc.marker)
			t.Setenv("RUNNER_TEMP", tc.runnerTemp)
			rt, fake := newManagedPolicyFake(t.TempDir())
			rt.euid = tc.euid
			err := runManagedPolicyE2E(rt, tc.args)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if fake.exec != "" {
				t.Fatal("re-exec must not happen on invalid input")
			}
		})
	}
}

func TestManagedPolicyE2EReexecsInFixtureUnderRunnerTemp(t *testing.T) {
	runnerTemp := t.TempDir()
	t.Setenv(managedPolicyMarker, managedPolicyEnabled)
	t.Setenv("RUNNER_TEMP", runnerTemp)
	rt, fake := newManagedPolicyFake(t.TempDir())
	args := []string{managedPolicyTestBinary, managedPolicyTestArg}
	if err := runManagedPolicyE2E(rt, args); err != nil {
		t.Fatal(err)
	}
	if fake.exec != "/usr/bin/unshare" {
		t.Fatalf("exec = %q, want /usr/bin/unshare", fake.exec)
	}
	requireUnshareInvocation(t, fake.argv, runnerTemp, args)
}

func TestCreateManagedPolicyFixtureRejectsUnexpectedOwner(t *testing.T) {
	_, err := createManagedPolicyFixture(t.TempDir(), uint32(os.Getuid())+1, uint32(os.Getgid()))
	if err == nil || !strings.Contains(err.Error(), "unexpected ownership or mode") {
		t.Fatalf("error = %v", err)
	}
}

func TestManagedPolicyNamespaceInstallsPolicyAndExecs(t *testing.T) {
	root, etc := t.TempDir(), t.TempDir()
	rt, fake := newManagedPolicyFake(etc)
	fake.namespace = managedPolicyChildNS
	upper, work := filepath.Join(root, "upper"), filepath.Join(root, "work")
	fake.mountInfo = overlayMountInfo(etc, upper, work)
	idFile := filepath.Join(root, managedPolicyIDFile)
	command := []string{managedPolicyTestBinary, managedPolicyTestArg}
	args := append([]string{root, idFile, managedPolicyParentNS}, command...)
	if err := runManagedPolicyNamespace(rt, args); err != nil {
		t.Fatal(err)
	}
	requireNamespaceID(t, idFile, managedPolicyChildNS)
	policy := filepath.Join(etc, "microsandbox", "managed.json")
	requireInstalledPolicy(t, policy)
	if !strings.Contains(fake.options, "upperdir="+upper) ||
		!strings.Contains(fake.options, "workdir="+work) {
		t.Fatalf("overlay options = %q", fake.options)
	}
	if fake.exec != managedPolicyTestBinary || !slices.Equal(fake.argv, command) {
		t.Fatalf("exec = %q argv = %q", fake.exec, fake.argv)
	}
	if !slices.Contains(fake.env, "DEVSY_TEST_MSB_MANAGED_POLICY="+policy) {
		t.Fatal("child env missing policy path")
	}
}

// namespaceViolation is one stage-2 invariant case; setup prepares the fake runtime and etc tree.
type namespaceViolation struct {
	name, want string
	args       []string
	setup      func(t *testing.T, etc string, rt *managedPolicyRuntime, fake *managedPolicyFake)
}

func runNamespaceViolations(t *testing.T, cases []namespaceViolation) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			etc := t.TempDir()
			rt, fake := newManagedPolicyFake(etc)
			tc.setup(t, etc, &rt, fake)
			err := runManagedPolicyNamespace(rt, tc.args)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if fake.exec != "" {
				t.Fatal("re-exec must not happen on invariant violation")
			}
		})
	}
}

func TestManagedPolicyNamespaceRejectsInvariantViolations(t *testing.T) {
	root := t.TempDir()
	upper, work := filepath.Join(root, "upper"), filepath.Join(root, "work")
	args := []string{
		root,
		filepath.Join(root, managedPolicyIDFile),
		managedPolicyParentNS,
		managedPolicyTestBinary,
	}
	noSetup := func(*testing.T, string, *managedPolicyRuntime, *managedPolicyFake) {}
	runNamespaceViolations(t, []namespaceViolation{
		{
			name: "not root", want: "managed-policy namespace must run as root", args: args,
			setup: func(_ *testing.T, _ string, rt *managedPolicyRuntime, _ *managedPolicyFake) {
				rt.euid = 1000
			},
		},
		{
			name:  "too few args",
			setup: noSetup,
			args:  args[:3],
			want:  "managed-policy-namespace requires <fixture-root> <namespace-id-file> <parent-namespace> <command>",
		},
		{
			name:  "namespace unchanged",
			want:  "mount namespace did not change",
			args:  args,
			setup: noSetup,
		},
		{
			name: "foreign overlay",
			args: args,
			setup: func(_ *testing.T, etc string, _ *managedPolicyRuntime, fake *managedPolicyFake) {
				fake.namespace = managedPolicyChildNS
				fake.mountInfo = overlayMountInfo(etc, "/elsewhere/upper", work)
			},
			want: "private /etc overlay does not use this fixture's owned upper and work directories",
		},
		{
			name: "existing microsandbox dir",
			args: args,
			want: "managed-policy fixture requires /etc/microsandbox to be absent in the overlay lower layer",
			setup: func(t *testing.T, etc string, _ *managedPolicyRuntime, fake *managedPolicyFake) {
				fake.namespace = managedPolicyChildNS
				fake.mountInfo = overlayMountInfo(etc, upper, work)
				if err := os.Mkdir(filepath.Join(etc, "microsandbox"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
	})
}

func TestHasOverlayMount(t *testing.T) {
	upper, work := "/fixture/upper", "/fixture/work"
	cases := []struct {
		name, mountInfo string
		want            bool
	}{
		{"matching overlay", overlayMountInfo("/etc", upper, work), true},
		{
			"upper and work on different lines",
			"36 25 0:31 / /etc rw - overlay overlay rw,upperdir=" + upper + "\n" +
				"37 25 0:32 / /etc rw - overlay overlay rw,workdir=" + work, false,
		},
		{"other mount point", overlayMountInfo("/other", upper, work), false},
		{
			"non-overlay fstype",
			"36 25 8:1 / /etc rw - ext4 /dev/sda1 rw,upperdir=" + upper + ",workdir=" + work,
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasOverlayMount(tc.mountInfo, "/etc", upper, work); got != tc.want {
				t.Fatalf("hasOverlayMount = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestManagedPolicyChildEnvironmentReplacesManagedKeys(t *testing.T) {
	ns := managedPolicyNamespace{
		root:    "/fixture",
		parent:  managedPolicyParentNS,
		current: managedPolicyChildNS,
	}
	environ := []string{
		managedPolicyPathEnv,
		"DEVSY_TEST_MSB_EXPECTED_UPPER=/stale/upper",
		managedPolicyMarker + "=false",
	}
	env := managedPolicyChildEnvironment(environ, ns, "/etc/microsandbox/managed.json")
	for _, want := range []string{
		managedPolicyPathEnv,
		managedPolicyMarker + "=true",
		"DEVSY_TEST_MSB_EXPECTED_UPPER=/fixture/upper",
		"DEVSY_TEST_MSB_MANAGED_POLICY=/etc/microsandbox/managed.json",
	} {
		if !slices.Contains(env, want) {
			t.Errorf("child env missing %q", want)
		}
	}
	if slices.Contains(env, "DEVSY_TEST_MSB_EXPECTED_UPPER=/stale/upper") ||
		slices.Contains(env, managedPolicyMarker+"=false") {
		t.Error("stale managed-policy value kept")
	}
}
