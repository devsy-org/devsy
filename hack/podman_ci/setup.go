package main

import (
	"context"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/spf13/cobra"
)

type FileSystem interface {
	ReadFile(string) ([]byte, error)
	Stat(string) (fs.FileInfo, error)
	OpenAppend(string) (io.WriteCloser, error)
}

type osFileSystem struct{}

func (osFileSystem) ReadFile(path string) ([]byte, error)  { return os.ReadFile(path) }
func (osFileSystem) Stat(path string) (fs.FileInfo, error) { return os.Stat(path) }
func (osFileSystem) OpenAppend(path string) (io.WriteCloser, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
}

type Dependencies struct {
	Runner Runner
	Clock  Clock
	Log    *Logger
	Files  FileSystem
	Budget *Budget
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "devsy-ci-podman",
		Short:         "CI helper for configuring and validating Podman test runtimes",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newSetupCommand())
	return root
}

func newSetupCommand() *cobra.Command {
	cfg := SetupConfig{
		PodmanPath:       platformDefaultPodmanPath(),
		CrunPath:         "/usr/local/bin/crun",
		BootstrapTimeout: 270 * time.Second,
		GitHubEnvPath:    os.Getenv("GITHUB_ENV"),
	}
	cmd := &cobra.Command{
		Use: "setup", Short: "Configure and validate a Podman CI runtime", Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			logger := newLogger()
			deps := Dependencies{
				Runner: &ExecRunner{Log: logger},
				Clock:  realClock{},
				Log:    logger,
				Files:  osFileSystem{},
			}
			return classifySetupError(setup(context.Background(), deps, cfg))
		},
	}
	cmd.Flags().StringVar(&cfg.PodmanPath, "podman-path", cfg.PodmanPath, "Podman executable path")
	cmd.Flags().
		StringVar(&cfg.CrunPath, "crun-path", cfg.CrunPath, "crun executable path (Linux only)")
	cmd.Flags().
		DurationVar(&cfg.BootstrapTimeout, "bootstrap-timeout", cfg.BootstrapTimeout, "maximum bootstrap duration")
	cmd.Flags().
		StringVar(&cfg.GitHubEnvPath, "github-env", cfg.GitHubEnvPath, "GitHub Actions environment file")
	cmd.Flags().Var(newModeValue(&cfg.Mode), "mode", "Podman mode: rootless or rootful")
	_ = cmd.MarkFlagRequired("mode")
	return cmd
}

type modeValue struct{ target *Mode }

func newModeValue(target *Mode) *modeValue { return &modeValue{target: target} }
func (v *modeValue) String() string        { return string(*v.target) }
func (v *modeValue) Set(value string) error {
	m := Mode(value)
	if m != ModeRootless && m != ModeRootful {
		return usageError("invalid mode %q: expected rootless or rootful", value)
	}
	*v.target = m
	return nil
}
func (v *modeValue) Type() string { return "mode" }

func setup(ctx context.Context, deps Dependencies, cfg SetupConfig) error {
	goos := runtimeGOOS()
	if goos == goosLinux || goos == goosWindows {
		if err := validateConfig(cfg, goos); err != nil {
			return err
		}
	}
	return platformSetup(ctx, deps, cfg)
}
