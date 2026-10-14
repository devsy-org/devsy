package dockerwsl

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework/runenv"
)

const (
	dockerWSLOwner        = "devsy-ci/docker-wsl-ssh"
	dockerWSLStateVersion = 1
	maxRunIDLen           = 64
	maxStateBytes         = 1 << 20
	maxTeardownErrorBytes = 512

	stateDirPerm  fs.FileMode = 0o700
	stateFilePerm fs.FileMode = 0o600
)

// Setup stages in acquisition order. stageVerified is the completion marker.
const (
	stageCreated       = "created"
	stageDistro        = "distro-verified"
	stagePackages      = "packages-installed"
	stageDockerd       = "dockerd-started"
	stageCLI           = "cli-installed"
	stageIdentity      = "ssh-identity-created"
	stageSSHServerConf = "ssh-server-configured"
	stageSSHD          = "sshd-started"
	stageSSHConfigured = "ssh-configured"
	stageVerified      = "verified"
)

var (
	setupStageOrder = []string{
		stageCreated, stageDistro, stagePackages, stageDockerd, stageCLI,
		stageIdentity, stageSSHServerConf, stageSSHD, stageSSHConfigured, stageVerified,
	}
	runIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	distroPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// ownedResources lists every resource this attempt created. Paths here are
// untrusted input to cleanup and are re-validated before use.
type ownedResources struct {
	DockerPidfile      string `json:"docker_pidfile,omitempty"`
	SSHDPidfile        string `json:"sshd_pidfile,omitempty"`
	SSHDConfigDir      string `json:"sshd_config_dir,omitempty"`
	CIUser             string `json:"ci_user,omitempty"`
	SSHIdentityFile    string `json:"ssh_identity_file,omitempty"`
	KnownHostsFile     string `json:"known_hosts_file,omitempty"`
	DockerCLIDirectory string `json:"docker_cli_directory,omitempty"`
	DockerBinDirectory string `json:"docker_bin_directory,omitempty"`
	DockerConfigDir    string `json:"docker_config_directory,omitempty"`

	// SSHConfig is the user's ssh_config this run prepended a block to.
	SSHConfig          string `json:"ssh_config,omitempty"`
	SSHConfigBackup    string `json:"ssh_config_backup,omitempty"`
	SSHConfigACLBackup string `json:"ssh_config_acl_backup,omitempty"`
	SSHConfigExisted   bool   `json:"ssh_config_existed,omitempty"`
	SSHConfigWritten   string `json:"ssh_config_written_sha256,omitempty"`
}

// teardownRecord notes one cleanup step attempt; Error is empty on success.
type teardownRecord struct {
	Step     string `json:"step"`
	Attempts int    `json:"attempts"`
	Error    string `json:"error,omitempty"`
}

// dockerWSLState is the non-secret, versioned record shared by setup, cleanup
// and the e2e preflight. It never holds key material or tokens.
type dockerWSLState struct {
	Version      int              `json:"version"`
	Owner        string           `json:"owner"`
	RunID        string           `json:"run_id"`
	Distribution string           `json:"distribution"`
	Stage        string           `json:"stage"`
	Owned        ownedResources   `json:"owned"`
	SSHHostAlias string           `json:"ssh_host_alias,omitempty"`
	SSHEndpoint  string           `json:"ssh_endpoint,omitempty"`
	DaemonID     string           `json:"daemon_id,omitempty"`
	Packages     []string         `json:"packages,omitempty"`
	Teardown     []teardownRecord `json:"teardown,omitempty"`
}

func newDockerWSLState(runID, distribution string) *dockerWSLState {
	return &dockerWSLState{
		Version:      dockerWSLStateVersion,
		Owner:        dockerWSLOwner,
		RunID:        runID,
		Distribution: distribution,
		Stage:        stageCreated,
	}
}

// stateStore reads and atomically writes one state file.
type stateStore struct {
	files runenv.FileStore
	path  string
}

func newStateStore(files runenv.FileStore, path string) stateStore {
	return stateStore{files: files, path: path}
}

// errStateMissing reports an absent state file.
var errStateMissing = errors.New("state file does not exist")

func (s stateStore) load() (*dockerWSLState, error) {
	data, err := s.files.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errStateMissing
	}
	if err != nil {
		return nil, fmt.Errorf("read state file %s: %w", s.path, err)
	}
	if len(data) > maxStateBytes {
		return nil, runenv.ConfigError("state file %s exceeds %d bytes", s.path, maxStateBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var st dockerWSLState
	if err := dec.Decode(&st); err != nil {
		return nil, runenv.ConfigError("state file %s is not valid state JSON: %v", s.path, err)
	}
	return &st, nil
}

// save writes the state through the FileStore's temp-file-and-rename path, so a
// reader never observes a half-written file.
func (s stateStore) save(st *dockerWSLState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	if err := s.files.MkdirAll(filepath.Dir(s.path), stateDirPerm); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	if err := s.files.WriteFile(s.path, append(data, '\n'), stateFilePerm); err != nil {
		return fmt.Errorf("write state file %s: %w", s.path, err)
	}
	return nil
}

func (s stateStore) remove() error {
	if err := s.files.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove state file %s: %w", s.path, err)
	}
	return nil
}

// stateGuard holds what cleanup trusts independently of the state file.
type stateGuard struct {
	runID      string
	stateDir   string
	userConfig string // the ssh_config path derived from the environment
}

// fixtureDir is the only Windows directory tree cleanup may delete from.
func fixtureDir(stateDir, runID string) string {
	return filepath.Join(stateDir, "run-"+runID)
}

func (g stateGuard) fixture() string { return fixtureDir(g.stateDir, g.runID) }

// validate rejects a state file that is the wrong version or owner, belongs to
// another run, or names any path outside the fixture roots.
func (g stateGuard) validate(st *dockerWSLState) error {
	switch {
	case st.Version != dockerWSLStateVersion:
		return runenv.ConfigError(
			"unsupported state version %d, want %d",
			st.Version,
			dockerWSLStateVersion,
		)
	case st.Owner != dockerWSLOwner:
		return runenv.ConfigError("state owner %q is not %q", st.Owner, dockerWSLOwner)
	case st.RunID != g.runID:
		return runenv.ConfigError("state belongs to run %q, this run is %q", st.RunID, g.runID)
	case !distroPattern.MatchString(st.Distribution):
		return runenv.ConfigError("state names an invalid distribution %q", st.Distribution)
	case !slices.Contains(setupStageOrder, st.Stage):
		return runenv.ConfigError("state has unknown stage %q", st.Stage)
	}
	if err := g.validateAlias(st); err != nil {
		return err
	}
	return g.validatePaths(st.Owned)
}

func (g stateGuard) validateAlias(st *dockerWSLState) error {
	if st.SSHHostAlias != "" && !hostAliasPattern.MatchString(st.SSHHostAlias) {
		return runenv.ConfigError("state names an invalid SSH host alias %q", st.SSHHostAlias)
	}
	if st.SSHEndpoint != "" && !strings.HasPrefix(st.SSHEndpoint, "ssh://"+ciUser+"@") {
		return runenv.ConfigError("state names an unexpected SSH endpoint %q", st.SSHEndpoint)
	}
	return nil
}

var hostAliasPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func (g stateGuard) validatePaths(o ownedResources) error {
	exact := []struct{ field, got, want string }{
		{"docker_pidfile", o.DockerPidfile, dockerPidfile},
		{"sshd_pidfile", o.SSHDPidfile, sshdPidfile},
		{"sshd_config_dir", o.SSHDConfigDir, sshdConfigDir},
		{"ci_user", o.CIUser, ciUser},
		{"ssh_config", o.SSHConfig, g.userConfig},
	}
	for _, c := range exact {
		if c.got != "" && c.got != c.want {
			return runenv.ConfigError("state field %s is %q, want %q", c.field, c.got, c.want)
		}
	}
	inside := []struct{ field, got string }{
		{"ssh_identity_file", o.SSHIdentityFile},
		{"known_hosts_file", o.KnownHostsFile},
		{"docker_cli_directory", o.DockerCLIDirectory},
		{"docker_bin_directory", o.DockerBinDirectory},
		{"docker_config_directory", o.DockerConfigDir},
		{"ssh_config_backup", o.SSHConfigBackup},
		{"ssh_config_acl_backup", o.SSHConfigACLBackup},
	}
	for _, c := range inside {
		if c.got != "" && !pathWithin(g.fixture(), c.got) {
			return runenv.ConfigError(
				"state field %s (%q) is outside %s",
				c.field,
				c.got,
				g.fixture(),
			)
		}
	}
	return nil
}

// pathWithin reports whether p lies strictly inside root, lexically.
func pathWithin(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != "." && filepath.IsLocal(rel)
}

func truncateError(err error) string {
	return runenv.TailBytes(runenv.Redact(err.Error()), maxTeardownErrorBytes)
}
