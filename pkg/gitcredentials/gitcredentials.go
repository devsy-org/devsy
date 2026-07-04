package gitcredentials

import (
	"context"
	"fmt"
	netURL "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/file"
	"github.com/devsy-org/devsy/pkg/git"
)

// GitCredentials is the git credential-helper record. Its wire form is git's
// documented key=value line protocol (see gitcredentials(7)).
type GitCredentials struct {
	Protocol string `json:"protocol,omitempty"`
	URL      string `json:"url,omitempty"`
	Host     string `json:"host,omitempty"`
	Path     string `json:"path,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// GitUser is a git identity (user.name / user.email).
type GitUser struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// Encode renders the credentials in git's key=value line format, terminated by
// a trailing newline. Only non-empty fields are emitted, matching git's helper
// protocol.
func (c GitCredentials) Encode() string {
	var b strings.Builder
	writeField := func(key, value string) {
		if value != "" {
			b.WriteString(key)
			b.WriteByte('=')
			b.WriteString(value)
			b.WriteByte('\n')
		}
	}
	writeField("protocol", c.Protocol)
	writeField("url", c.URL)
	writeField("path", c.Path)
	writeField("host", c.Host)
	writeField("username", c.Username)
	writeField("password", c.Password)
	return b.String()
}

// ParseCredentials parses git's key=value credential line format. It is the
// inverse of Encode. Unknown keys are ignored, matching git's tolerant parsing.
func ParseCredentials(raw string) GitCredentials {
	var c GitCredentials
	fields := map[string]*string{
		"protocol": &c.Protocol,
		"url":      &c.URL,
		"path":     &c.Path,
		"host":     &c.Host,
		"username": &c.Username,
		"password": &c.Password,
	}
	for line := range strings.SplitSeq(raw, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		if field, known := fields[key]; known {
			*field = value
		}
	}
	return c
}

// credentialHelperValue builds the `credential.helper` value that runs devsy's
// own credential helper subcommand.
func credentialHelperValue(binaryPath string, port int) string {
	helper := fmt.Sprintf(`!'%s' internal agent git-credentials`, binaryPath)
	if port != -1 {
		helper += fmt.Sprintf(` --port %d`, port)
	}
	return helper
}

// ConfigureHelper installs credential helper into the user's global git
// config, replacing any previously configured helper.
func ConfigureHelper(ctx context.Context, binaryPath, userName string, port int) error {
	gitConfigPath, err := getGlobalGitConfigPath(userName)
	if err != nil {
		return err
	}

	cfg := git.At("").Config()
	scope := git.ScopeFile(gitConfigPath)
	helper := credentialHelperValue(binaryPath, port)

	existing, err := cfg.GetAll(ctx, "credential.helper", scope)
	if err != nil {
		return err
	}
	if len(existing) == 1 && existing[0] == helper {
		return nil
	}

	if err := cfg.UnsetAll(ctx, "credential.helper", scope); err != nil {
		return err
	}
	if err := cfg.Add(ctx, "credential.helper", helper, scope); err != nil {
		return err
	}

	return file.Chown(userName, gitConfigPath)
}

// RemoveHelper removes credential helper from the user's global config.
func RemoveHelper(ctx context.Context, userName string) error {
	gitConfigPath, err := getGlobalGitConfigPath(userName)
	if err != nil {
		return err
	}
	return RemoveHelperFromPath(ctx, gitConfigPath)
}

// RemoveHelperFromPath removes the credential.helper setting from the config at
// path, leaving any other credential.* settings intact.
func RemoveHelperFromPath(ctx context.Context, gitConfigPath string) error {
	return git.At("").Config().
		UnsetAll(ctx, "credential.helper", git.ScopeFile(gitConfigPath))
}

// SetUser sets the global git identity for the given OS user.
func SetUser(ctx context.Context, userName string, user *GitUser) error {
	scope, err := identityScope(userName)
	if err != nil {
		return err
	}

	cfg := git.At("").Config()
	fields := []struct{ key, value string }{
		{"user.name", user.Name},
		{"user.email", user.Email},
	}
	wrote := false
	for _, f := range fields {
		if f.value == "" {
			continue
		}
		if err := cfg.Set(ctx, f.key, f.value, scope); err != nil {
			return err
		}
		wrote = true
	}

	// Only reassign ownership when we actually wrote the named user's config.
	// Chowning unconditionally would fail on the not-yet-created file when
	// neither field was provided.
	if wrote && userName != "" {
		path, err := getGlobalGitConfigPath(userName)
		if err != nil {
			return err
		}
		return file.Chown(userName, path)
	}
	return nil
}

// GetUser reads the git identity visible from workingDir, or the named user's
// global identity when workingDir is empty.
func GetUser(ctx context.Context, userName, workingDir string) (*GitUser, error) {
	scope := git.ScopeGlobal
	if workingDir != "" {
		scope = git.ScopeDefault
	}
	if userName != "" {
		path, err := getGlobalGitConfigPath(userName)
		if err != nil {
			return nil, fmt.Errorf("get git global dir for %s: %w", userName, err)
		}
		scope = git.ScopeFile(path)
	}

	cfg := git.At(workingDir).Config()
	name, _ := cfg.Get(ctx, "user.name", scope)
	email, _ := cfg.Get(ctx, "user.email", scope)
	return &GitUser{Name: name, Email: email}, nil
}

// identityScope resolves the config scope for setting a user's identity: the
// named user's global config file, or the current user's global config.
func identityScope(userName string) (git.ConfigScope, error) {
	if userName == "" {
		return git.ScopeGlobal, nil
	}
	path, err := getGlobalGitConfigPath(userName)
	if err != nil {
		return git.ConfigScope{}, err
	}
	return git.ScopeFile(path), nil
}

// GetCredentials resolves credentials for a request, using devsy's credential
// server when a helper port is configured, otherwise `git credential fill`.
func GetCredentials(ctx context.Context, request *GitCredentials) (*GitCredentials, error) {
	if port := os.Getenv(config.EnvGitHelperPort); port != "" {
		return credentialsViaServer(ctx, request, port)
	}
	return credentialsViaGit(ctx, request)
}

// credentialsViaServer asks devsy's own credential helper process (used inside
// agent containers, addressed by port).
func credentialsViaServer(
	ctx context.Context,
	request *GitCredentials,
	port string,
) (*GitCredentials, error) {
	binaryPath, err := os.Executable()
	if err != nil {
		return nil, err
	}

	//nolint:gosec // binaryPath is from os.Executable(), not user input
	cmd := exec.CommandContext(
		ctx,
		binaryPath,
		"internal", "agent", "git-credentials", "--port", port, "get",
	)
	cmd.Stdin = strings.NewReader(request.Encode())
	stdout, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	creds := ParseCredentials(string(stdout))
	return &creds, nil
}

// credentialsViaGit resolves credentials through `git credential fill`, which
// consults the host's configured credential helpers.
func credentialsViaGit(ctx context.Context, request *GitCredentials) (*GitCredentials, error) {
	stdout, err := git.At("", git.WithStrictHostKeyChecking(false)).
		CredentialFill(ctx, request.Encode())
	if err != nil {
		return nil, err
	}
	creds := ParseCredentials(stdout)
	return &creds, nil
}

// Resolver adapts GetCredentials to download.CredentialResolver, letting the
// download package authenticate private assets without depending on this
// package (dependency inversion).
type Resolver struct{}

// Resolve implements download.CredentialResolver.
func (Resolver) Resolve(
	ctx context.Context,
	protocol, host, path string,
) (username, password string, err error) {
	creds, err := GetCredentials(ctx, &GitCredentials{Protocol: protocol, Host: host, Path: path})
	if err != nil || creds == nil {
		return "", "", err
	}
	return creds.Username, creds.Password, nil
}

type GetHttpPathParameters struct {
	Host        string
	Protocol    string
	CurrentPath string
	Repository  string
}

// GetHTTPPath returns the repository path component when git's
// `credential.<url>.useHttpPath` is enabled for the host+protocol, else "".
func GetHTTPPath(ctx context.Context, params GetHttpPathParameters) (string, error) {
	if params.CurrentPath != "" {
		return params.CurrentPath, nil
	}

	configKey := fmt.Sprintf("credential.%s://%s.useHttpPath", params.Protocol, params.Host)
	value, _ := git.At("", git.WithStrictHostKeyChecking(false)).
		Config().Get(ctx, configKey, git.ScopeDefault)
	if value != config.BoolTrue {
		return "", nil
	}

	parsed, err := netURL.Parse(params.Repository)
	if err != nil {
		return "", fmt.Errorf("parse workspace repository: %w", err)
	}
	return parsed.Path, nil
}

// SetupGpgGitKey sets the global signing key.
func SetupGpgGitKey(ctx context.Context, gitSignKey string) error {
	if err := git.At("").Config().
		Set(ctx, "user.signingKey", gitSignKey, git.ScopeGlobal); err != nil {
		return fmt.Errorf("git signkey: %w", err)
	}
	return nil
}

// getGlobalGitConfigPath resolves the global git config for the specified user
// per https://git-scm.com/docs/git-config/#Documentation/git-config.txt-XDGCONFIGHOMEgitconfig
func getGlobalGitConfigPath(userName string) (string, error) {
	if xdgConfigHome := os.Getenv("XDG_CONFIG_HOME"); xdgConfigHome != "" {
		return filepath.Join(xdgConfigHome, "git", "config"), nil
	}

	home, err := command.GetHome(userName)
	if err != nil {
		return "", fmt.Errorf("get homedir for %s: %w", userName, err)
	}
	return filepath.Join(home, ".gitconfig"), nil
}

// GetLocalGitConfigPath resolves the local git config for a repository path.
func GetLocalGitConfigPath(repoPath string) string {
	return filepath.Join(repoPath, ".git", "config")
}
