package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ConfigScope selects which git configuration file an operation targets.
type ConfigScope struct {
	flag string
	file string
}

var (
	ScopeDefault = ConfigScope{}
	ScopeLocal = ConfigScope{flag: "--local"}
	ScopeGlobal = ConfigScope{flag: flagGlobal}
	ScopeSystem = ConfigScope{flag: flagSystem}
)

// ScopeFile targets a specific config file.
func ScopeFile(path string) ConfigScope {
	return ConfigScope{flag: flagFile, file: path}
}

func (s ConfigScope) args() []string {
	switch {
	case s.flag == "":
		return nil
	case s.file != "":
		return []string{s.flag, s.file}
	default:
		return []string{s.flag}
	}
}

// Config exposes `git config` operations scoped to a repository. Obtain one via
// Repo.Config.
type Config struct {
	repo *Repo
}

// Config returns a handle for `git config` operations on the repository.
func (r *Repo) Config() *Config {
	return &Config{repo: r}
}

// Get retrieves the value of a config key in the given scope. An absent key is not an error: it returns ("", nil).
func (c *Config) Get(ctx context.Context, key string, scope ConfigScope) (string, error) {
	args := append([]string{subConfig}, scope.args()...)
	args = append(args, flagGet, key)
	res, err := c.repo.run(ctx, args...)
	value := strings.TrimSpace(string(res.Stdout))
	if err != nil {
		// `git config --get` exits with code 1 when the key does not exist.
		var cmdErr *CommandError
		if errors.As(err, &cmdErr) && cmdErr.ExitCode == 1 {
			return "", nil
		}
		return value, fmt.Errorf("get git config %q: %w", key, err)
	}
	return value, nil
}

// Add sets a config key to value in the given scope.
func (c *Config) Add(ctx context.Context, key, value string, scope ConfigScope) error {
	args := append([]string{subConfig}, scope.args()...)
	args = append(args, "--add", key, value)
	if _, err := c.repo.run(ctx, args...); err != nil {
		return fmt.Errorf("add git config %q: %w", key, err)
	}
	return nil
}

// Set sets a config key to value in the given scope, replacing any existing value.
func (c *Config) Set(ctx context.Context, key, value string, scope ConfigScope) error {
	args := append([]string{subConfig}, scope.args()...)
	args = append(args, key, value)
	if _, err := c.repo.run(ctx, args...); err != nil {
		return fmt.Errorf("set git config %q: %w", key, err)
	}
	return nil
}

// Unset removes a config key in the given scope.
func (c *Config) Unset(ctx context.Context, key string, scope ConfigScope) error {
	args := append([]string{subConfig}, scope.args()...)
	args = append(args, "--unset", key)
	if _, err := c.repo.run(ctx, args...); err != nil {
		return fmt.Errorf("unset git config %q: %w", key, err)
	}
	return nil
}

// UnsetAll removes all values of a multi-valued config key in the given scope. An absent key is not an error.
func (c *Config) UnsetAll(ctx context.Context, key string, scope ConfigScope) error {
	args := append([]string{subConfig}, scope.args()...)
	args = append(args, "--unset-all", key)
	if _, err := c.repo.run(ctx, args...); err != nil {
		// Exit code 5 means the key (or section) does not exist.
		var cmdErr *CommandError
		if errors.As(err, &cmdErr) && cmdErr.ExitCode == 5 {
			return nil
		}
		return fmt.Errorf("unset-all git config %q: %w", key, err)
	}
	return nil
}

// GetAll retrieves all values of a multi-valued config key in the given scope.
// An absent key is not an error: it returns (nil, nil).
func (c *Config) GetAll(ctx context.Context, key string, scope ConfigScope) ([]string, error) {
	args := append([]string{subConfig}, scope.args()...)
	args = append(args, flagGet+"-all", key)
	res, err := c.repo.run(ctx, args...)
	if err != nil {
		// Exit code 1 means the key does not exist.
		var cmdErr *CommandError
		if errors.As(err, &cmdErr) && cmdErr.ExitCode == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("get-all git config %q: %w", key, err)
	}
	return splitLines(res.Stdout), nil
}
