package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

const (
	commandInstallKind  = "install-kind"
	kindClusterNameSize = 16
)

type ClusterNamer func() (string, error)

type ClusterCreator func(ctx context.Context, kindPath, name, image string) error

type KindConfig struct {
	URL        string
	SHA256     string
	KindPath   string
	Image      string
	GitHubPath string
}

func newInstallKindCommand() *cobra.Command {
	var cfg KindConfig
	cmd := &cobra.Command{
		Use:   commandInstallKind,
		Short: "Install a verified kind binary and create a cluster with a random name",
		RunE: func(*cobra.Command, []string) error {
			deps := Dependencies{
				Log:            newLogger(),
				Files:          osFileSystem{},
				Download:       downloadFile,
				NewClusterName: newClusterName,
				CreateCluster:  createKindCluster,
			}
			return classifyOperationalError(installKind(context.Background(), deps, cfg))
		},
	}
	cmd.Flags().StringVar(&cfg.URL, "url", "", "kind download URL")
	cmd.Flags().StringVar(&cfg.SHA256, "sha256", "", "expected SHA256 of the kind binary")
	cmd.Flags().StringVar(&cfg.KindPath, "kind-path", "", "local path for the kind binary")
	cmd.Flags().StringVar(&cfg.Image, "image", "", "kind node image")
	cmd.Flags().
		StringVar(&cfg.GitHubPath, "github-path", os.Getenv("GITHUB_PATH"), "file to append the kind directory to")
	for _, name := range []string{"url", "sha256", "kind-path", "image"} {
		_ = cmd.MarkFlagRequired(name)
	}
	return cmd
}

func installKind(ctx context.Context, deps Dependencies, cfg KindConfig) error {
	if _, statErr := deps.Files.Stat(cfg.KindPath); statErr != nil {
		if err := deps.Download(ctx, cfg.URL, cfg.KindPath); err != nil {
			return fmt.Errorf("download kind: %w", err)
		}
	}
	data, err := deps.Files.ReadFile(cfg.KindPath)
	if err != nil {
		return fmt.Errorf("read kind: %w", err)
	}
	if err := verifyChecksum("kind.exe", data, cfg.SHA256); err != nil {
		return err
	}
	deps.Log.Printf("kind.exe checksum verified: %s", installSum(data))

	if err := appendGitHubPath(deps.Files, cfg.GitHubPath, filepath.Dir(cfg.KindPath)); err != nil {
		return err
	}

	name, err := deps.NewClusterName()
	if err != nil {
		return fmt.Errorf("generate kind cluster name: %w", err)
	}
	deps.Log.Printf("creating kind cluster %s", name)
	if err := deps.CreateCluster(ctx, cfg.KindPath, name, cfg.Image); err != nil {
		return fmt.Errorf("kind create cluster: %w", err)
	}
	return nil
}

func newClusterName() (string, error) {
	b := make([]byte, kindClusterNameSize)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func createKindCluster(ctx context.Context, kindPath, name, image string) error {
	cmd := exec.CommandContext(ctx, kindPath, "create", "cluster", "--name", name, "--image", image)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
