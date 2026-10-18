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
	managedPolicyMarker      = "DEVSY_REQUIRE_MSB_MANAGED_POLICY_FIXTURE"
	managedPolicyTestBinary  = "./e2e.test"
	managedPolicyEtcRoot     = "/etc"
	managedPolicyDirName     = "microsandbox"
	managedPolicyFileName    = "managed.json"
	managedPolicyFixturePfx  = "devsy-managed-policy."
	managedPolicyNamespaceID = "mount-namespace-id"
	managedPolicyDocument    = "{\"version\":1,\"overrides\":{}}\n"
)

// managedPolicyOwner is the uid/gid required for fixture directories and the policy file.
type managedPolicyOwner struct {
	uid uint32
	gid uint32
}

// managedPolicyRuntime isolates every privileged side effect so the invariants
// can be exercised without root or a real mount namespace.
type managedPolicyRuntime struct {
	euid           int
	owner          managedPolicyOwner
	etcRoot        string
	mountNamespace func() (string, error)
	mountOverlay   func(target, options string) error
	readMountInfo  func() (string, error)
	lookPath       func(name string) (string, error)
	executable     func() (string, error)
	replace        func(path string, argv, env []string) error
}

// managedPolicyNamespace is the state handed from the outer wrapper to the
// unshared namespace stage.
type managedPolicyNamespace struct {
	root    string
	idFile  string
	parent  string
	current string
	command []string
}

func productionManagedPolicyRuntime() managedPolicyRuntime {
	return managedPolicyRuntime{
		euid:           os.Geteuid(),
		owner:          managedPolicyOwner{},
		etcRoot:        managedPolicyEtcRoot,
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

// managedPolicyE2ECLI runs stage one: validate inputs, create the fixture, and
// re-exec this binary under a private mount namespace. It only returns on error.
func managedPolicyE2ECLI(args []string, stderr io.Writer) int {
	syscall.Umask(0o077)
	return managedPolicyExit(stderr, runManagedPolicyE2E(productionManagedPolicyRuntime(), args))
}

// managedPolicyNamespaceCLI runs stage two inside the unshared mount namespace.
// It is invoked only by managedPolicyE2ECLI.
func managedPolicyNamespaceCLI(args []string, stderr io.Writer) int {
	syscall.Umask(0o077)
	return managedPolicyExit(
		stderr,
		runManagedPolicyNamespace(productionManagedPolicyRuntime(), args),
	)
}

func managedPolicyExit(stderr io.Writer, err error) int {
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runManagedPolicyE2E(rt managedPolicyRuntime, args []string) error {
	if err := checkManagedPolicyInputs(rt, args); err != nil {
		return err
	}
	root, err := createManagedPolicyFixture(os.Getenv("RUNNER_TEMP"), rt.owner)
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
	argv := []string{
		"unshare",
		"--mount",
		"--propagation",
		"private",
		"--",
		self,
		managedPolicyNamespaceCommand,
		root,
		filepath.Join(root, managedPolicyNamespaceID),
		parent,
	}
	argv = append(argv, args...)
	if err := rt.replace(unshare, argv, os.Environ()); err != nil {
		return fmt.Errorf("exec unshare: %w", err)
	}
	return nil
}

func checkManagedPolicyInputs(rt managedPolicyRuntime, args []string) error {
	if rt.euid != 0 {
		return errors.New("managed-policy fixture must run as root")
	}
	if os.Getenv(managedPolicyMarker) != "true" {
		return errors.New("managed-policy E2E wrapper requires the CI expectation marker")
	}
	if !isExistingDirectory(os.Getenv("RUNNER_TEMP")) {
		return errors.New("RUNNER_TEMP must name an existing directory")
	}
	if len(args) == 0 || args[0] != managedPolicyTestBinary {
		return errors.New("expected the already-built ./e2e.test binary as the first argument")
	}
	return nil
}

func isExistingDirectory(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// createManagedPolicyFixture creates <runnerTemp>/devsy-managed-policy.* with
// upper and work subdirectories, all owned by owner with mode 0700.
func createManagedPolicyFixture(runnerTemp string, owner managedPolicyOwner) (string, error) {
	parent := strings.TrimRight(runnerTemp, "/")
	if parent == "" {
		parent = "/"
	}
	root, err := os.MkdirTemp(parent, managedPolicyFixturePfx)
	if err != nil {
		return "", fmt.Errorf("create managed-policy fixture: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return "", fmt.Errorf("chmod managed-policy fixture: %w", err)
	}
	upper, work := filepath.Join(root, "upper"), filepath.Join(root, "work")
	for _, dir := range []string{upper, work} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			return "", fmt.Errorf("create managed-policy fixture directory: %w", err)
		}
	}
	for _, dir := range []string{root, upper, work} {
		if err := verifyManagedPolicyDirectory(dir, owner); err != nil {
			return "", err
		}
	}
	return root, nil
}

func verifyManagedPolicyDirectory(path string, owner managedPolicyOwner) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect managed-policy fixture directory %s: %w", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != owner.uid || stat.Gid != owner.gid || stat.Mode&0o7777 != 0o700 {
		return fmt.Errorf(
			"managed-policy fixture directory has unexpected ownership or mode: %s",
			path,
		)
	}
	return nil
}

func runManagedPolicyNamespace(rt managedPolicyRuntime, args []string) error {
	if rt.euid != 0 {
		return errors.New("managed-policy namespace must run as root")
	}
	ns, err := enterManagedPolicyNamespace(rt, args)
	if err != nil {
		return err
	}
	upper, work := filepath.Join(ns.root, "upper"), filepath.Join(ns.root, "work")
	options := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", rt.etcRoot, upper, work)
	if err := rt.mountOverlay(rt.etcRoot, options); err != nil {
		return fmt.Errorf("mount overlay on %s: %w", rt.etcRoot, err)
	}
	if err := verifyOverlayMount(rt, upper, work); err != nil {
		return err
	}
	if err := ensureManagedPolicyDirAbsent(rt.etcRoot); err != nil {
		return err
	}
	policy, err := writeInitialManagedPolicy(rt.etcRoot, rt.owner)
	if err != nil {
		return err
	}
	env := managedPolicyChildEnvironment(os.Environ(), ns, policy)
	if err := rt.replace(ns.command[0], ns.command, env); err != nil {
		return fmt.Errorf("exec %s: %w", ns.command[0], err)
	}
	return nil
}

// enterManagedPolicyNamespace validates the stage-two arguments, confirms the
// mount namespace changed, and records its id for the test process.
func enterManagedPolicyNamespace(
	rt managedPolicyRuntime,
	args []string,
) (managedPolicyNamespace, error) {
	if len(args) < 4 {
		return managedPolicyNamespace{}, errors.New(
			"managed-policy-namespace requires <fixture-root> <namespace-id-file> <parent-namespace> <command>",
		)
	}
	ns := managedPolicyNamespace{root: args[0], idFile: args[1], parent: args[2], command: args[3:]}
	current, err := rt.mountNamespace()
	if err != nil {
		return ns, fmt.Errorf("read mount namespace: %w", err)
	}
	if current == ns.parent {
		return ns, errors.New("mount namespace did not change")
	}
	ns.current = current
	if err := os.WriteFile(ns.idFile, []byte(current+"\n"), 0o600); err != nil {
		return ns, fmt.Errorf("write mount namespace id: %w", err)
	}
	return ns, nil
}

func verifyOverlayMount(rt managedPolicyRuntime, upper, work string) error {
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

// hasOverlayMount reports whether a mountinfo line shows an overlay at mountPoint
// whose super options name exactly the upper and work directories.
func hasOverlayMount(mountInfo, mountPoint, upper, work string) bool {
	for line := range strings.SplitSeq(mountInfo, "\n") {
		if overlayLineMatches(strings.Fields(line), mountPoint, upper, work) {
			return true
		}
	}
	return false
}

// overlayLineMatches reports whether one mountinfo line's overlay options name
// the given upper and work directories.
func overlayLineMatches(fields []string, mountPoint, upper, work string) bool {
	if len(fields) < 5 || fields[4] != mountPoint {
		return false
	}
	sep := lastSeparatorIndex(fields)
	if sep < 0 || sep+3 >= len(fields) || fields[sep+1] != "overlay" {
		return false
	}
	options := strings.Split(fields[sep+3], ",")
	return slices.Contains(options, "upperdir="+upper) && slices.Contains(options, "workdir="+work)
}

func lastSeparatorIndex(fields []string) int {
	sep := -1
	for i, field := range fields {
		if field == "-" {
			sep = i
		}
	}
	return sep
}

func ensureManagedPolicyDirAbsent(etcRoot string) error {
	_, err := os.Lstat(filepath.Join(etcRoot, managedPolicyDirName))
	if err == nil {
		return errors.New(
			"managed-policy fixture requires /etc/microsandbox to be absent in the overlay lower layer",
		)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("inspect /etc/microsandbox: %w", err)
}

// writeInitialManagedPolicy creates <etcRoot>/microsandbox/managed.json with the
// empty version-1 policy, written via an exclusive temp file and renamed into place.
func writeInitialManagedPolicy(etcRoot string, owner managedPolicyOwner) (string, error) {
	dir := filepath.Join(etcRoot, managedPolicyDirName)
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	if err := setManagedPolicyOwner(dir, owner, 0o755); err != nil {
		return "", err
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".managed.json.initial.%d", os.Getpid()))
	if err := createManagedPolicyTemp(tmp); err != nil {
		return "", fmt.Errorf("write initial managed policy: %w", err)
	}
	if err := setManagedPolicyOwner(tmp, owner, 0o600); err != nil {
		return "", err
	}
	policy := filepath.Join(dir, managedPolicyFileName)
	if err := os.Rename(tmp, policy); err != nil {
		return "", fmt.Errorf("install managed policy: %w", err)
	}
	return policy, nil
}

func createManagedPolicyTemp(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(managedPolicyDocument)
	return errors.Join(writeErr, file.Close())
}

func setManagedPolicyOwner(path string, owner managedPolicyOwner, mode os.FileMode) error {
	if err := os.Chown(path, int(owner.uid), int(owner.gid)); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// managedPolicyChildEnvironment returns environ with every managed-policy key
// replaced by the values the e2e test binary's consumer expects.
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
	managed := make(map[string]bool, len(overrides))
	for _, assignment := range overrides {
		key, _, _ := strings.Cut(assignment, "=")
		managed[key] = true
	}
	env := make([]string, 0, len(environ)+len(overrides))
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		if !managed[key] {
			env = append(env, entry)
		}
	}
	return append(env, overrides...)
}
