package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	msiexecPath       = "msiexec.exe"
	podmanExe         = "podman.exe"
	msiInstallTimeout = 4 * time.Minute
	commandInstall    = "install"
	msiLogTailLines   = 50
	podmanSearchDepth = 4
)

type Downloader func(ctx context.Context, url, dest string) error

type InstallConfig struct {
	URL        string
	SHA256     string
	MSIPath    string
	InstallDir string
	LogPath    string
	GitHubPath string
}

func newInstallCommand() *cobra.Command {
	var cfg InstallConfig
	cmd := &cobra.Command{
		Use:   commandInstall,
		Short: "Install Podman from a verified MSI",
		RunE: func(*cobra.Command, []string) error {
			logger := newLogger()
			deps := Dependencies{
				Runner:   &ExecRunner{Log: logger},
				Log:      logger,
				Files:    osFileSystem{},
				Download: downloadFile,
			}
			return classifyOperationalError(platformInstall(context.Background(), deps, cfg))
		},
	}
	cmd.Flags().StringVar(&cfg.URL, "url", "", "podman MSI download URL")
	cmd.Flags().StringVar(&cfg.SHA256, "sha256", "", "expected SHA256 of the podman MSI")
	cmd.Flags().StringVar(&cfg.MSIPath, "msi-path", "", "local path for the podman MSI")
	cmd.Flags().StringVar(&cfg.InstallDir, "install-dir", "", "msiexec INSTALLDIR")
	cmd.Flags().StringVar(&cfg.LogPath, "log-path", "", "msiexec verbose log path")
	cmd.Flags().
		StringVar(&cfg.GitHubPath, "github-path", os.Getenv("GITHUB_PATH"), "file to append the install dir to")
	for _, name := range []string{"url", "sha256", "msi-path", "install-dir", "log-path"} {
		_ = cmd.MarkFlagRequired(name)
	}
	return cmd
}

func installPodmanMSI(
	ctx context.Context,
	deps Dependencies,
	cfg InstallConfig,
	searchRoot string,
) error {
	// An MSI restored by actions/cache sits at MSIPath; the checksum below still guards it.
	if _, statErr := deps.Files.Stat(cfg.MSIPath); statErr != nil {
		if err := deps.Download(ctx, cfg.URL, cfg.MSIPath); err != nil {
			return fmt.Errorf("download podman MSI: %w", err)
		}
	}

	msi, err := deps.Files.ReadFile(cfg.MSIPath)
	if err != nil {
		return fmt.Errorf("read podman MSI: %w", err)
	}
	if err := verifyChecksum("podman MSI", msi, cfg.SHA256); err != nil {
		return err
	}
	deps.Log.Printf("Podman MSI checksum verified: %s", installSum(msi))

	res := deps.Runner.Run(ctx, CommandSpec{
		Path:    msiexecPath,
		CmdLine: msiexecCommandLine(cfg),
		Timeout: msiInstallTimeout,
	})
	deps.Log.Printf("MSI exit code: %s", exitCodeString(res.ExitCode))

	podman := filepath.Join(cfg.InstallDir, podmanExe)
	if _, err := deps.Files.Stat(podman); err != nil {
		logMissingPodman(deps, cfg, searchRoot)
		return fmt.Errorf("podman.exe not found at %s", cfg.InstallDir)
	}

	return appendGitHubPath(deps.Files, cfg.GitHubPath, cfg.InstallDir)
}

func msiexecCommandLine(cfg InstallConfig) string {
	return fmt.Sprintf(`%s /i "%s" /qn /norestart /l*v "%s" INSTALLDIR="%s"`,
		msiexecPath, cfg.MSIPath, cfg.LogPath, cfg.InstallDir)
}

func installSum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func verifyChecksum(artifact string, data []byte, expected string) error {
	actual := installSum(data)
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf(
			"SHA256 mismatch for %s! Expected: %s, Got: %s",
			artifact,
			expected,
			actual,
		)
	}
	return nil
}

func logMissingPodman(deps Dependencies, cfg InstallConfig, searchRoot string) {
	deps.Log.Printf("podman.exe not found at %s", cfg.InstallDir)
	deps.Log.Printf("MSI install log (last %d lines):", msiLogTailLines)
	if data, err := deps.Files.ReadFile(cfg.LogPath); err != nil {
		deps.Log.Printf("install log unavailable: %v", err)
	} else {
		deps.Log.Printf("%s", tailLines(data, msiLogTailLines))
	}
	found := findFilesNamed(searchRoot, podmanExe, podmanSearchDepth)
	deps.Log.Printf("podman.exe found under %s:", searchRoot)
	for _, path := range found {
		deps.Log.Printf("%s", path)
	}
}

func tailLines(data []byte, n int) string {
	text := strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	lines := strings.Split(text, "\n")
	lines = lines[max(0, len(lines)-n):]
	return strings.Join(lines, "\n")
}

// findFilesNamed mirrors PowerShell Get-ChildItem -Depth: items up to maxDepth+1 levels below root.
func findFilesNamed(root, name string, maxDepth int) []string {
	var found []string
	rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		depth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - rootDepth
		if d.IsDir() && depth > maxDepth {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == name {
			found = append(found, path)
		}
		return nil
	})
	return found
}

func appendGitHubPath(files FileSystem, githubPath, dir string) error {
	if githubPath == "" {
		return nil
	}
	w, err := files.OpenAppend(githubPath)
	if err != nil {
		return fmt.Errorf("open GITHUB_PATH: %w", err)
	}
	_, writeErr := io.WriteString(w, dir+"\n")
	if err := errors.Join(writeErr, w.Close()); err != nil {
		return fmt.Errorf("write GITHUB_PATH: %w", err)
	}
	return nil
}

func downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		_ = os.Remove(dest)
		return err
	}
	return nil
}
