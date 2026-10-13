//go:build linux

package up

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	safeRecreateManagedPolicyPath = "/etc/microsandbox/managed.json"
	safeRecreateEnabledValue      = "true"
)

type safeRecreateSandboxDefaults struct {
	MemoryMiB uint64 `json:"memory_mib"`
}

type safeRecreatePolicyOverrides struct {
	SandboxDefaults *safeRecreateSandboxDefaults `json:"sandbox_defaults,omitempty"`
}

type safeRecreateManagedPolicy struct {
	Version   int                         `json:"version"`
	Overrides safeRecreatePolicyOverrides `json:"overrides"`
}

type safeRecreateManagedFixturePaths struct {
	policyPath string
	root       string
	upper      string
	work       string
}

// setSafeRecreateManagedMemory3072 raises memory above the explicit 2048 MiB
// boot ceiling only after managed precedence is applied.
func setSafeRecreateManagedMemory3072() error {
	policy := safeRecreateManagedPolicy{
		Version: 1,
		Overrides: safeRecreatePolicyOverrides{
			SandboxDefaults: &safeRecreateSandboxDefaults{MemoryMiB: 3072},
		},
	}
	return writeSafeRecreateManagedPolicy(policy)
}

// clearSafeRecreateManagedPolicy restores the valid empty policy before cleanup.
func clearSafeRecreateManagedPolicy() error {
	return writeSafeRecreateManagedPolicy(safeRecreateManagedPolicy{Version: 1})
}

func writeSafeRecreateManagedPolicy(policy safeRecreateManagedPolicy) error {
	paths, err := verifySafeRecreateManagedPolicyFixture()
	if err != nil {
		return err
	}
	data, err := json.Marshal(policy)
	if err != nil {
		return fmt.Errorf("encode managed policy: %w", err)
	}
	return publishSafeRecreateManagedPolicy(paths.policyPath, append(data, '\n'))
}

func publishSafeRecreateManagedPolicy(path string, data []byte) (returnErr error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".managed.json.tmp-")
	if err != nil {
		return fmt.Errorf("create managed policy temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err := os.Remove(tmpName); err != nil && !errors.Is(err, os.ErrNotExist) {
			returnErr = errors.Join(
				returnErr,
				fmt.Errorf("remove managed policy temporary file: %w", err),
			)
		}
	}()
	if err := writeSafeRecreateManagedPolicyTemp(tmp, data); err != nil {
		return err
	}
	if err := verifySafeRecreateRootFile(tmpName, 0o600); err != nil {
		return fmt.Errorf("verify managed policy temporary file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace managed policy atomically: %w", err)
	}
	if _, err := verifySafeRecreateManagedPolicyFixture(); err != nil {
		return fmt.Errorf("verify managed policy after replacement: %w", err)
	}
	return nil
}

func writeSafeRecreateManagedPolicyTemp(tmp *os.File, data []byte) (returnErr error) {
	defer func() { returnErr = errors.Join(returnErr, tmp.Close()) }()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("set managed policy mode: %w", err)
	}
	if err := tmp.Chown(0, 0); err != nil {
		return fmt.Errorf("set managed policy ownership: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write managed policy: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync managed policy: %w", err)
	}
	return nil
}

func verifySafeRecreateManagedPolicyFixture() (safeRecreateManagedFixturePaths, error) {
	if os.Geteuid() != 0 {
		return safeRecreateManagedFixturePaths{}, errors.New(
			"managed policy fixture requires euid 0",
		)
	}
	paths, err := safeRecreateManagedPolicyPathsFromEnv()
	if err != nil {
		return safeRecreateManagedFixturePaths{}, err
	}
	if err := verifySafeRecreateManagedFixtureDirectories(paths); err != nil {
		return safeRecreateManagedFixturePaths{}, err
	}
	if err := verifySafeRecreateManagedFixtureNamespace(); err != nil {
		return safeRecreateManagedFixturePaths{}, err
	}
	if err := verifySafeRecreateEtcOverlay(paths.upper, paths.work); err != nil {
		return safeRecreateManagedFixturePaths{}, err
	}
	if err := verifySafeRecreateManagedPolicyTarget(paths.policyPath); err != nil {
		return safeRecreateManagedFixturePaths{}, err
	}
	return paths, nil
}

func safeRecreateManagedPolicyPathsFromEnv() (safeRecreateManagedFixturePaths, error) {
	paths := safeRecreateManagedFixturePaths{
		policyPath: os.Getenv("DEVSY_TEST_MSB_MANAGED_POLICY"),
		root:       os.Getenv("DEVSY_TEST_MSB_FIXTURE_ROOT"),
		upper:      os.Getenv("DEVSY_TEST_MSB_EXPECTED_UPPER"),
		work:       os.Getenv("DEVSY_TEST_MSB_EXPECTED_WORK"),
	}
	if paths.policyPath != safeRecreateManagedPolicyPath {
		return safeRecreateManagedFixturePaths{}, fmt.Errorf(
			"managed policy fixture path must be %q",
			safeRecreateManagedPolicyPath,
		)
	}
	if paths.root == "" || paths.upper != filepath.Join(paths.root, "upper") ||
		paths.work != filepath.Join(paths.root, "work") {
		return safeRecreateManagedFixturePaths{}, errors.New(
			"managed policy fixture root and overlay paths are inconsistent",
		)
	}
	return paths, nil
}

func verifySafeRecreateManagedFixtureDirectories(paths safeRecreateManagedFixturePaths) error {
	for _, directory := range []string{paths.root, paths.upper, paths.work} {
		if err := verifySafeRecreateRootDirectory(directory, 0o700); err != nil {
			return fmt.Errorf("verify owned overlay directory: %w", err)
		}
	}
	return nil
}

func verifySafeRecreateManagedFixtureNamespace() error {
	parentNS := os.Getenv("DEVSY_TEST_MSB_PARENT_MNT_NS")
	expectedNS := os.Getenv("DEVSY_TEST_MSB_EXPECTED_MNT_NS")
	currentNS, err := os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		return fmt.Errorf("read current mount namespace: %w", err)
	}
	if parentNS == "" || expectedNS == "" || parentNS == currentNS || expectedNS != currentNS {
		return errors.New("managed policy fixture is outside its verified private mount namespace")
	}
	return nil
}

func verifySafeRecreateManagedPolicyTarget(path string) error {
	if err := verifySafeRecreateRootDirectory(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return verifySafeRecreateRootFile(path, 0o600)
}

func verifySafeRecreateRootDirectory(path string, mode os.FileMode) error {
	// #nosec G703 -- Callers pass fixed policy parents or validated owned overlay paths.
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if !info.IsDir() || info.Mode().Perm() != mode {
		return fmt.Errorf("%s must be a real directory with mode %04o", path, mode)
	}
	return verifySafeRecreateOwner(info, path)
}

func verifySafeRecreateRootFile(path string, mode os.FileMode) error {
	// #nosec G703 -- Callers pass the fixed policy path or its CreateTemp-owned sibling.
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != mode {
		return fmt.Errorf("%s must be a regular file with mode %04o", path, mode)
	}
	return verifySafeRecreateOwner(info, path)
}

func verifySafeRecreateOwner(info os.FileInfo, path string) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || stat.Gid != 0 {
		return fmt.Errorf("%s must be owned by uid 0 and gid 0", path)
	}
	return nil
}

func verifySafeRecreateEtcOverlay(upper, work string) error {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return fmt.Errorf("read mount table: %w", err)
	}
	return verifySafeRecreateEtcOverlayMount(data, upper, work)
}

func verifySafeRecreateEtcOverlayMount(data []byte, upper, work string) error {
	for line := range strings.SplitSeq(string(data), "\n") {
		before, after, found := strings.Cut(line, " - ")
		if !found {
			continue
		}
		fields := strings.Fields(before)
		if len(fields) < 6 || unescapeMountInfo(fields[4]) != "/etc" {
			continue
		}
		post := strings.Fields(after)
		if len(post) < 3 || post[0] != "overlay" {
			return errors.New("/etc is not mounted as overlay")
		}
		return verifySafeRecreateOverlayOptions(post[2], upper, work)
	}
	return errors.New("/etc does not have a mountinfo entry in this namespace")
}

func verifySafeRecreateOverlayOptions(options, upper, work string) error {
	expected := map[string]string{
		"upperdir": filepath.Clean(upper),
		"workdir":  filepath.Clean(work),
	}
	seen := make(map[string]bool, len(expected))
	for option := range strings.SplitSeq(options, ",") {
		key, value, hasValue := strings.Cut(option, "=")
		want, owned := expected[key]
		if !owned {
			continue
		}
		if !hasValue || seen[key] || unescapeMountInfo(value) != want {
			return fmt.Errorf("/etc overlay %s is not owned by this fixture", key)
		}
		seen[key] = true
	}
	if len(seen) != len(expected) {
		return errors.New("/etc overlay is missing its owned upper or work directory")
	}
	return nil
}

func unescapeMountInfo(value string) string {
	for _, escape := range []struct{ encoded, decoded string }{
		{`\040`, " "}, {`\011`, "\t"}, {`\012`, "\n"}, {`\134`, `\`},
	} {
		value = strings.ReplaceAll(value, escape.encoded, escape.decoded)
	}
	return value
}
