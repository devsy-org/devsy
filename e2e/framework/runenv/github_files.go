package runenv

import (
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"strings"
)

const githubFilePerm fs.FileMode = 0o600

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// GitHubFiles appends to the GITHUB_ENV and GITHUB_PATH special files. An empty
// path means the helper runs outside Actions: the value is printed (redacted)
// to Out instead, so local runs need no GitHub context.
type GitHubFiles struct {
	EnvPath  string
	PathPath string
	Files    FileStore
	Out      io.Writer
}

// SetEnv exports NAME=value to later workflow steps. Invalid names and values
// containing line breaks or NUL are rejected: they could inject extra entries.
func (g GitHubFiles) SetEnv(name, value string) error {
	if !envNamePattern.MatchString(name) {
		return ConfigError("invalid environment variable name %q", name)
	}
	if err := rejectControl("value of "+name, value); err != nil {
		return err
	}
	if g.EnvPath == "" {
		return g.print("env", name+"="+value)
	}
	return g.append(g.EnvPath, name+"="+value+"\n")
}

// AddPath prepends dir to PATH for later workflow steps.
func (g GitHubFiles) AddPath(dir string) error {
	if dir == "" {
		return ConfigError("empty PATH entry")
	}
	if err := rejectControl("PATH entry", dir); err != nil {
		return err
	}
	if g.PathPath == "" {
		return g.print("path", dir)
	}
	return g.append(g.PathPath, dir+"\n")
}

func (g GitHubFiles) append(path, line string) error {
	if err := g.Files.AppendFile(path, []byte(line), githubFilePerm); err != nil {
		return fmt.Errorf("append to %s: %w", path, err)
	}
	return nil
}

func (g GitHubFiles) print(kind, line string) error {
	if g.Out == nil {
		return nil
	}
	_, err := fmt.Fprintf(g.Out, "local %s: %s\n", kind, Redact(line))
	return err
}

func rejectControl(what, value string) error {
	if strings.ContainsAny(value, "\r\n\x00") {
		return ConfigError("%s contains a line break or NUL byte", what)
	}
	return nil
}
