//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

const (
	managedPolicyMarker     = "DEVSY_REQUIRE_MSB_MANAGED_POLICY_FIXTURE"
	managedPolicyTestBinary = "./e2e.test"
	managedPolicyDocument   = "{\"version\":1,\"overrides\":{}}\n"
	managedPolicyIDFile     = "mount-namespace-id"
)

// managedPolicyRuntime isolates privileged side effects so tests can run the invariants unprivileged.
type managedPolicyRuntime struct {
	euid           int
	uid, gid       uint32
	etcRoot        string
	mountNamespace func() (string, error)
	mountOverlay   func(target, options string) error
	readMountInfo  func() (string, error)
	lookPath       func(string) (string, error)
	executable     func() (string, error)
	replace        func(path string, argv, env []string) error
}

// managedPolicyNamespace is the namespace state handed to the test command.
type managedPolicyNamespace struct {
	root, parent, current string
}

// productionManagedPolicyRuntime leaves uid and gid zero, so fixture and policy files are root-owned.
func productionManagedPolicyRuntime() managedPolicyRuntime {
	return managedPolicyRuntime{
		euid:           os.Geteuid(),
		etcRoot:        "/etc",
		mountNamespace: func() (string, error) { return os.Readlink("/proc/self/ns/mnt") },
		mountOverlay: func(target, options string) error {
			return syscall.Mount("overlay", target, "overlay", 0, options)
		},
		readMountInfo: func() (string, error) {
			data, err := os.ReadFile("/proc/self/mountinfo")
			return string(data), err
		},
		lookPath:   exec.LookPath,
		executable: os.Executable,
		replace:    syscall.Exec,
	}
}

// managedPolicyExit runs one stage under a private umask; a successful stage never returns.
func managedPolicyExit(
	stderr io.Writer,
	stage func(managedPolicyRuntime, []string) error,
	args []string,
) int {
	syscall.Umask(0o077)
	if err := stage(productionManagedPolicyRuntime(), args); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func managedPolicyE2ECLI(args []string, stderr io.Writer) int {
	return managedPolicyExit(stderr, runManagedPolicyE2E, args)
}

func managedPolicyNamespaceCLI(args []string, stderr io.Writer) int {
	return managedPolicyExit(stderr, runManagedPolicyNamespace, args)
}

// runManagedPolicyE2E creates the fixture and re-execs this binary inside a private mount namespace.
func runManagedPolicyE2E(rt managedPolicyRuntime, args []string) error {
	runnerTemp := os.Getenv("RUNNER_TEMP")
	if err := checkManagedPolicyInputs(rt, runnerTemp, args); err != nil {
		return err
	}
	root, err := createManagedPolicyFixture(runnerTemp, rt.uid, rt.gid)
	if err != nil {
		return err
	}
	parent, err := rt.mountNamespace()
	if err != nil {
		return fmt.Errorf("read parent mount namespace: %w", err)
	}
	self, err := rt.executable()
	if err != nil {
		return fmt.Errorf("resolve managed-policy wrapper: %w", err)
	}
	unshare, err := rt.lookPath("unshare")
	if err != nil {
		return fmt.Errorf("find unshare: %w", err)
	}
	argv := append([]string{
		"unshare", "--mount", "--propagation", "private", "--", self,
		managedPolicyNamespaceCommand, root, filepath.Join(root, managedPolicyIDFile), parent,
	}, args...)
	if err := rt.replace(unshare, argv, os.Environ()); err != nil {
		return fmt.Errorf("exec unshare: %w", err)
	}
	return nil
}

func checkManagedPolicyInputs(rt managedPolicyRuntime, runnerTemp string, args []string) error {
	info, statErr := os.Stat(runnerTemp)
	switch {
	case rt.euid != 0:
		return errors.New("managed-policy fixture must run as root")
	case os.Getenv(managedPolicyMarker) != "true":
		return errors.New("managed-policy E2E wrapper requires the CI expectation marker")
	case statErr != nil || !info.IsDir():
		return errors.New("RUNNER_TEMP must name an existing directory")
	case len(args) == 0 || args[0] != managedPolicyTestBinary:
		return errors.New("expected the already-built ./e2e.test binary as the first argument")
	}
	return nil
}

// createManagedPolicyFixture creates <runnerTemp>/devsy-managed-policy.* with upper and work
// subdirectories. The CLI umask keeps every directory at 0700; ownership is then verified.
func createManagedPolicyFixture(runnerTemp string, uid, gid uint32) (string, error) {
	root, err := os.MkdirTemp(filepath.Clean(runnerTemp), "devsy-managed-policy.")
	if err != nil {
		return "", fmt.Errorf("create managed-policy fixture: %w", err)
	}
	dirs := []string{root, filepath.Join(root, "upper"), filepath.Join(root, "work")}
	for _, dir := range dirs[1:] {
		if err := os.Mkdir(dir, 0o700); err != nil {
			return "", fmt.Errorf("create managed-policy fixture directory: %w", err)
		}
	}
	for _, dir := range dirs {
		if err := verifyManagedPolicyDirectory(dir, uid, gid); err != nil {
			return "", err
		}
	}
	return root, nil
}

// verifyManagedPolicyDirectory requires dir to be owned by uid:gid with mode 0700.
func verifyManagedPolicyDirectory(dir string, uid, gid uint32) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("inspect managed-policy fixture directory %s: %w", dir, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uid || stat.Gid != gid || stat.Mode&0o7777 != 0o700 {
		return fmt.Errorf(
			"managed-policy fixture directory has unexpected ownership or mode: %s",
			dir,
		)
	}
	return nil
}

// runManagedPolicyNamespace runs inside the unshared namespace: it overlays /etc, installs the
// initial policy, and execs the test command.
func runManagedPolicyNamespace(rt managedPolicyRuntime, args []string) error {
	ns, err := enterManagedPolicyNamespace(rt, args)
	if err != nil {
		return err
	}
	if err := mountManagedPolicyOverlay(rt, ns.root); err != nil {
		return err
	}
	policy, err := installManagedPolicy(rt)
	if err != nil {
		return err
	}
	command := args[3:]
	if err := rt.replace(
		command[0],
		command,
		managedPolicyChildEnvironment(os.Environ(), ns, policy),
	); err != nil {
		return fmt.Errorf("exec %s: %w", command[0], err)
	}
	return nil
}

// enterManagedPolicyNamespace checks that the mount namespace changed and records its id.
func enterManagedPolicyNamespace(
	rt managedPolicyRuntime,
	args []string,
) (managedPolicyNamespace, error) {
	if rt.euid != 0 {
		return managedPolicyNamespace{}, errors.New("managed-policy namespace must run as root")
	}
	if len(args) < 4 {
		return managedPolicyNamespace{}, errors.New(
			"managed-policy-namespace requires <fixture-root> <namespace-id-file> <parent-namespace> <command>",
		)
	}
	ns := managedPolicyNamespace{root: args[0], parent: args[2]}
	current, err := rt.mountNamespace()
	if err != nil {
		return ns, fmt.Errorf("read mount namespace: %w", err)
	}
	if current == ns.parent {
		return ns, errors.New("mount namespace did not change")
	}
	ns.current = current
	if err := os.WriteFile(args[1], []byte(current+"\n"), 0o600); err != nil {
		return ns, fmt.Errorf("write mount namespace id: %w", err)
	}
	return ns, nil
}

// mountManagedPolicyOverlay overlays etc before any managed-policy path is created, so no host
// file changes, and verifies the overlay uses this fixture's upper and work directories.
func mountManagedPolicyOverlay(rt managedPolicyRuntime, root string) error {
	upper, work := filepath.Join(root, "upper"), filepath.Join(root, "work")
	options := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", rt.etcRoot, upper, work)
	if err := rt.mountOverlay(rt.etcRoot, options); err != nil {
		return fmt.Errorf("mount overlay on %s: %w", rt.etcRoot, err)
	}
	mountInfo, err := rt.readMountInfo()
	if err != nil {
		return fmt.Errorf("read mountinfo: %w", err)
	}
	if !hasOverlayMount(mountInfo, rt.etcRoot, upper, work) {
		return errors.New(
			"private /etc overlay does not use this fixture's owned upper and work directories",
		)
	}
	return nil
}

// installManagedPolicy creates <etc>/microsandbox/managed.json holding the empty version-1 policy.
func installManagedPolicy(rt managedPolicyRuntime) (string, error) {
	dir := filepath.Join(rt.etcRoot, "microsandbox")
	if err := createManagedPolicyDir(rt, dir); err != nil {
		return "", err
	}
	return writeManagedPolicy(rt, dir)
}

// createManagedPolicyDir requires the directory to be absent from the lower layer, then creates it 0755.
func createManagedPolicyDir(rt managedPolicyRuntime, dir string) error {
	_, err := os.Lstat(dir)
	if err == nil {
		return errors.New(
			"managed-policy fixture requires /etc/microsandbox to be absent in the overlay lower layer",
		)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect %s: %w", dir, err)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return setOwnerMode(dir, rt.uid, rt.gid, 0o755)
}

// writeManagedPolicy writes the policy to an exclusive temp file and renames it into place.
func writeManagedPolicy(rt managedPolicyRuntime, dir string) (string, error) {
	tmp := filepath.Join(dir, fmt.Sprintf(".managed.json.initial.%d", os.Getpid()))
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("write initial managed policy: %w", err)
	}
	_, writeErr := file.WriteString(managedPolicyDocument)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return "", fmt.Errorf("write initial managed policy: %w", err)
	}
	if err := setOwnerMode(tmp, rt.uid, rt.gid, 0o600); err != nil {
		return "", err
	}
	policy := filepath.Join(dir, "managed.json")
	if err := os.Rename(tmp, policy); err != nil {
		return "", fmt.Errorf("install managed policy: %w", err)
	}
	return policy, nil
}

func setOwnerMode(path string, uid, gid uint32, mode os.FileMode) error {
	if err := os.Chown(path, int(uid), int(gid)); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// hasOverlayMount reports whether one mountinfo line shows an overlay at mountPoint whose super
// options name exactly the upper and work directories. Both must share a line.
func hasOverlayMount(mountInfo, mountPoint, upper, work string) bool {
	for line := range strings.SplitSeq(mountInfo, "\n") {
		options := overlaySuperOptions(line, mountPoint)
		if options != nil && slices.Contains(options, "upperdir="+upper) &&
			slices.Contains(options, "workdir="+work) {
			return true
		}
	}
	return false
}

// overlaySuperOptions returns the super options of a mountinfo line that is an overlay at mountPoint,
// or nil. The single "-" separator splits optional fields from the fstype and source fields.
func overlaySuperOptions(line, mountPoint string) []string {
	fields := strings.Fields(line)
	sep := slices.Index(fields, "-")
	if len(fields) < 5 || fields[4] != mountPoint || sep < 0 || sep+3 >= len(fields) ||
		fields[sep+1] != "overlay" {
		return nil
	}
	return strings.Split(fields[sep+3], ",")
}

// managedPolicyChildEnvironment replaces every managed-policy key in environ with the values
// the e2e test binary expects.
func managedPolicyChildEnvironment(
	environ []string,
	ns managedPolicyNamespace,
	policy string,
) []string {
	overrides := []string{
		managedPolicyMarker + "=true",
		"DEVSY_TEST_MSB_MANAGED_POLICY=" + policy,
		"DEVSY_TEST_MSB_FIXTURE_ROOT=" + ns.root,
		"DEVSY_TEST_MSB_PARENT_MNT_NS=" + ns.parent,
		"DEVSY_TEST_MSB_EXPECTED_MNT_NS=" + ns.current,
		"DEVSY_TEST_MSB_EXPECTED_UPPER=" + filepath.Join(ns.root, "upper"),
		"DEVSY_TEST_MSB_EXPECTED_WORK=" + filepath.Join(ns.root, "work"),
	}
	kept := slices.DeleteFunc(slices.Clone(environ), func(entry string) bool {
		key, _, _ := strings.Cut(entry, "=")
		return slices.ContainsFunc(
			overrides,
			func(o string) bool { return strings.HasPrefix(o, key+"=") },
		)
	})
	return append(kept, overrides...)
}
