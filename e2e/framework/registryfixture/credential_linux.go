//go:build linux

package registryfixture

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

const (
	// #nosec G101 -- This is an environment variable name, not a credential value.
	CredentialConfigEnv = "CASE_A_CREDENTIAL_CONFIG"
	// #nosec G101 -- This is an environment variable name, not a credential value.
	CredentialLogEnv = "CASE_A_CREDENTIAL_OBSERVATIONS"
	// #nosec G101 -- This is an environment variable name, not a credential value.
	CredentialRootEnv      = "CASE_A_CREDENTIAL_ROOT"
	CredentialOperationEnv = "CASE_A_OPERATION_ID"
	HelperBinaryName       = "docker-credential-fixture"
)

// CredentialConfig fields are stored only in the private fixture root.
type CredentialConfig struct {
	OperationID            string `json:"operation_id"`
	ServerKey              string `json:"server_key"`
	Username               string `json:"username,omitempty"`
	Secret                 string `json:"secret,omitempty"`
	CredentialsAvailable   bool   `json:"credentials_available"`
	ExpectedProviderSHA256 string `json:"expected_provider_sha256"`
}

// CredentialObservation contains secret-free helper invocation evidence.
type CredentialObservation struct {
	OperationID string          `json:"operation_id"`
	ServerKey   string          `json:"server_key"`
	Action      string          `json:"action"`
	Parent      ProcessIdentity `json:"parent"`
	Outcome     string          `json:"outcome"`
}

// ProcessIdentity records a live process identity without cmdline or env.
type ProcessIdentity struct {
	PID            int    `json:"pid"`
	StartTimeTicks uint64 `json:"start_time_ticks"`
	Executable     string `json:"executable"`
	SHA256         string `json:"sha256"`
}

const credentialsNotFoundMessage = "credentials not found in native keychain"

var (
	errCredentialRequestRead = errors.New("credential request could not be read")
	errCredentialRequestKey  = errors.New("credential request did not name the owned registry")
)

type credentialSession struct {
	root, log string
	config    CredentialConfig
}

// CredentialHelperMain implements Docker's credential-helper `get` wire
// protocol. The input is recorded only as an owned server key and parent PID;
// the secret is read from a private fixture file and never logged.
func CredentialHelperMain(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if !validCredentialHelperArgs(args) {
		_, _ = fmt.Fprintln(stderr, "fixture credential helper supports get only")
		return 1
	}
	if !credentialEnvironmentConfigured() {
		_, _ = fmt.Fprintln(stderr, "fixture credential helper is not configured")
		return 1
	}
	session, err := loadCredentialConfig()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "fixture credential configuration is invalid")
		return 1
	}
	outcome, ok := session.observeRequest(stdin, stderr)
	if !ok {
		return 1
	}
	if outcome == "parent_hash_mismatch" {
		_, _ = fmt.Fprintln(stderr, "credential helper parent is not the expected provider")
		return 1
	}
	if !session.config.CredentialsAvailable {
		_, _ = fmt.Fprintln(stdout, credentialsNotFoundMessage)
		return 1
	}
	if err := writeCredentials(stdout, session.config); err != nil {
		return 1
	}
	return 0
}

func validCredentialHelperArgs(args []string) bool {
	return len(args) == 1 && args[0] == "get"
}

func (s credentialSession) observeRequest(stdin io.Reader, stderr io.Writer) (string, bool) {
	serverKey, err := readCredentialRequest(stdin, s.config.ServerKey)
	if err != nil {
		if errors.Is(err, errCredentialRequestRead) {
			_, _ = fmt.Fprintln(stderr, "fixture credential request could not be read")
		} else {
			_, _ = fmt.Fprintln(stderr, "credential request did not name the owned registry")
		}
		return "", false
	}
	parent, err := captureParentIdentity(os.Getppid())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "credential helper could not verify its live Linux parent")
		return "", false
	}
	outcome := credentialOutcome(parent.SHA256, s.config)
	if err := s.recordObservation(serverKey, parent, outcome); err != nil {
		_, _ = fmt.Fprintln(stderr, "fixture observation could not be recorded")
		return "", false
	}
	return outcome, true
}

func credentialEnvironmentConfigured() bool {
	return os.Getenv(CredentialRootEnv) != "" && os.Getenv(CredentialConfigEnv) != "" &&
		os.Getenv(CredentialLogEnv) != "" && os.Getenv(CredentialOperationEnv) != ""
}

func loadCredentialConfig() (credentialSession, error) {
	root, configPath := os.Getenv(CredentialRootEnv), os.Getenv(CredentialConfigEnv)
	logPath, operationID := os.Getenv(CredentialLogEnv), os.Getenv(CredentialOperationEnv)
	if root == "" || configPath == "" || logPath == "" || operationID == "" {
		return credentialSession{}, errors.New("credential helper is not configured")
	}
	cfg, err := ReadCredentialConfig(root, configPath)
	if err != nil {
		return credentialSession{}, err
	}
	if err := validateCredentialConfig(cfg, operationID); err != nil {
		return credentialSession{}, errors.New("credential configuration is invalid")
	}
	return credentialSession{root: root, log: logPath, config: cfg}, nil
}

func validateCredentialConfig(cfg CredentialConfig, operationID string) error {
	if cfg.OperationID == "" || cfg.OperationID != operationID || cfg.ServerKey == "" ||
		!validSHA256(cfg.ExpectedProviderSHA256) {
		return errors.New("credential configuration is invalid")
	}
	if cfg.CredentialsAvailable && (cfg.Username == "" || cfg.Secret == "") {
		return errors.New("available fixture credentials are incomplete")
	}
	return nil
}

func readCredentialRequest(stdin io.Reader, expected string) (string, error) {
	serverKey, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("%w: %w", errCredentialRequestRead, err)
	}
	serverKey = strings.TrimSpace(serverKey)
	if serverKey != expected {
		return "", errCredentialRequestKey
	}
	return serverKey, nil
}

func credentialOutcome(parentHash string, cfg CredentialConfig) string {
	switch {
	case !equalSHA256(parentHash, cfg.ExpectedProviderSHA256):
		return "parent_hash_mismatch"
	case cfg.CredentialsAvailable:
		return "credentials_provided"
	default:
		return "credentials_not_found"
	}
}

func (s credentialSession) recordObservation(
	serverKey string,
	parent ProcessIdentity,
	outcome string,
) error {
	line, err := json.Marshal(CredentialObservation{
		OperationID: s.config.OperationID,
		ServerKey:   serverKey,
		Action:      "get",
		Parent:      parent,
		Outcome:     outcome,
	})
	if err != nil {
		return err
	}
	return appendOwnedLine(s.root, s.log, append(line, '\n'))
}

func writeCredentials(stdout io.Writer, cfg CredentialConfig) error {
	result, err := json.Marshal(map[string]string{"Username": cfg.Username, "Secret": cfg.Secret})
	if err != nil {
		return err
	}
	_, err = stdout.Write(append(result, '\n'))
	return err
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func equalSHA256(a, b string) bool { return strings.EqualFold(a, b) }

// captureParentIdentity binds an observation to the live direct parent without
// reading its command line or environment, which may contain credentials.
func captureParentIdentity(pid int) (ProcessIdentity, error) {
	if runtime.GOOS != "linux" || pid <= 0 {
		return ProcessIdentity{}, errors.New("linux /proc identity is required")
	}
	proc := filepath.Join("/proc", fmt.Sprint(pid))
	startBefore, err := processStartTime(filepath.Join(proc, "stat"))
	if err != nil {
		return ProcessIdentity{}, err
	}
	executable, err := os.Readlink(filepath.Join(proc, "exe"))
	if err != nil {
		return ProcessIdentity{}, err
	}
	digest, err := hashFile(filepath.Join(proc, "exe"))
	if err != nil {
		return ProcessIdentity{}, err
	}
	startAfter, err := processStartTime(filepath.Join(proc, "stat"))
	if err != nil {
		return ProcessIdentity{}, err
	}
	if startBefore != startAfter {
		return ProcessIdentity{}, errors.New("parent process changed during identity capture")
	}
	return ProcessIdentity{
		PID: pid, StartTimeTicks: startBefore, Executable: executable, SHA256: digest,
	}, nil
}

func hashFile(path string) (string, error) {
	// Parent hashing receives only /proc/<verified-pid>/exe.
	//nolint:gosec // fixed procfs executable entry for the verified parent PID
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, file)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func processStartTime(path string) (uint64, error) {
	// The caller supplies only /proc/<verified-pid>/stat.
	//nolint:gosec // fixed procfs stat entry for the verified parent PID
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	// /proc/<pid>/stat field 2 is parenthesized and may contain spaces or ')'.
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 || end+1 >= len(b) {
		return 0, errors.New("invalid Linux process stat")
	}
	fields := strings.Fields(string(b[end+1:])) // starts at field 3 (state)
	if len(fields) <= 19 {                      // field 22 is index 19 from field 3
		return 0, errors.New("linux process stat lacks start time")
	}
	var start uint64
	if _, err := fmt.Sscan(fields[19], &start); err != nil || start == 0 {
		return 0, errors.New("invalid Linux process start time")
	}
	return start, nil
}

func ownedChild(root, path string) (string, error) {
	if err := validatePrivateRoot(root); err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("owned file path must be absolute and clean")
	}
	if filepath.Dir(path) != root || filepath.Base(path) == "." ||
		filepath.Base(path) == string(filepath.Separator) {
		return "", errors.New("owned file must be a direct child of its private root")
	}
	return path, nil
}

func validatePrivateRoot(root string) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return errors.New("owned root must be absolute and clean")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return errors.New("owned root must be canonical and symlink-free")
	}
	return nil
}

func openOwnedFile(root, path string, flags int, create bool) (file *os.File, resultErr error) {
	defer closeFileOnError(&file, &resultErr)
	if _, err := ownedChild(root, path); err != nil {
		return nil, err
	}
	openedRoot, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, openedRoot.Close()) }()
	rootFile, err := openedRoot.Open(".")
	if err != nil {
		return nil, errors.New("owned root must be a mode-0700 directory")
	}
	defer func() { resultErr = errors.Join(resultErr, rootFile.Close()) }()
	if err := validateOpenedRoot(rootFile); err != nil {
		return nil, err
	}
	flags = ownedFileOpenFlags(flags, create)
	file, err = openedRoot.OpenFile(filepath.Base(path), flags, 0o600)
	if err != nil {
		return nil, err
	}
	if err := validateOpenedFile(file); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func closeFileOnError(file **os.File, resultErr *error) {
	if *resultErr == nil || *file == nil {
		return
	}
	*resultErr = errors.Join(*resultErr, (*file).Close())
	*file = nil
}

func ownedFileOpenFlags(flags int, create bool) int {
	flags |= syscall.O_NOFOLLOW | syscall.O_CLOEXEC
	if create {
		flags |= syscall.O_CREAT | syscall.O_EXCL
	}
	return flags
}

func validateOpenedRoot(root *os.File) error {
	info, err := root.Stat()
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("owned root must be a mode-0700 directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return errors.New("owned root has an unexpected owner")
	}
	return nil
}

func validateOpenedFile(file *os.File) error {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("owned file must be a mode-0600 regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return errors.New("owned file has an unexpected owner")
	}
	return nil
}

// ReadCredentialConfig reads a validated private fixture config. The path must
// be a direct child of a canonical, current-user-owned mode-0700 root.
func ReadCredentialConfig(root, path string) (CredentialConfig, error) {
	f, err := openOwnedFile(root, path, syscall.O_RDONLY, false)
	if err != nil {
		return CredentialConfig{}, err
	}
	var cfg CredentialConfig
	err = json.NewDecoder(io.LimitReader(f, 64*1024)).Decode(&cfg)
	return cfg, errors.Join(err, f.Close())
}

// IsCredentialHelperInvocation recognizes only the fixed helper path under
// the configured private root, when it resolves to this running test binary.
func IsCredentialHelperInvocation(argv0 string) bool {
	root := os.Getenv(CredentialRootEnv)
	if root == "" {
		return false
	}
	path, ok := helperInvocationPath(argv0)
	if !ok || validatePrivateRoot(root) != nil {
		return false
	}
	helperPath := filepath.Join(root, "bin", HelperBinaryName)
	if path != helperPath || !privateDirectory(root) ||
		!privateDirectory(filepath.Dir(helperPath)) {
		return false
	}
	return sameAsCurrentExecutable(helperPath)
}

func helperInvocationPath(argv0 string) (string, bool) {
	path := argv0
	if !filepath.IsAbs(argv0) {
		if argv0 != HelperBinaryName {
			return "", false
		}
		var err error
		path, err = exec.LookPath(argv0)
		if err != nil {
			return "", false
		}
	}
	return path, filepath.IsAbs(path) && filepath.Clean(path) == path
}

func sameAsCurrentExecutable(path string) bool {
	// path is constructed from the validated private fixture root and fixed name.
	//nolint:gosec // the helper path is constrained by IsCredentialHelperInvocation
	linkInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	executable, err := os.Executable()
	if err != nil {
		return false
	}
	executableInfo, err := os.Stat(executable)
	return err == nil && os.SameFile(linkInfo, executableInfo)
}

// DispatchCredentialHelper runs the helper only for the fixed private-root
// path when it resolves to the current executable. handled is false for the
// normal test-binary invocation.
func DispatchCredentialHelper() (handled bool, exitCode int) {
	if !IsCredentialHelperInvocation(os.Args[0]) {
		return false, 0
	}
	return true, CredentialHelperMain(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}

func privateDirectory(path string) bool {
	if !validPrivateDirectoryPath(path) {
		return false
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return false
	}
	rootFile, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return false
	}
	valid := privateDirectoryOwner(rootFile)
	fileCloseErr := rootFile.Close()
	rootCloseErr := root.Close()
	return valid && fileCloseErr == nil && rootCloseErr == nil
}

func validPrivateDirectoryPath(path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return false
	}
	// path is either the validated fixture root or its fixed bin child.
	//nolint:gosec // constrained to the private root/bin pair by the caller
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 &&
		info.Mode().Perm() == 0o700
}

func privateDirectoryOwner(rootFile *os.File) bool {
	info, err := rootFile.Stat()
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}

func appendOwnedLine(root, path string, line []byte) error {
	f, err := openOwnedFile(root, path, syscall.O_WRONLY|syscall.O_APPEND, false)
	if errors.Is(err, os.ErrNotExist) {
		f, err = openOwnedFile(root, path, syscall.O_WRONLY|syscall.O_APPEND, true)
		if errors.Is(err, os.ErrExist) {
			f, err = openOwnedFile(root, path, syscall.O_WRONLY|syscall.O_APPEND, false)
		}
	}
	if err != nil {
		return err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return errors.Join(err, f.Close())
	}
	_, writeErr := f.Write(line)
	unlockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return errors.Join(writeErr, unlockErr, f.Close())
}
