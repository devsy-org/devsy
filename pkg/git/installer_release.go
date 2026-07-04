package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/download"
	"github.com/devsy-org/devsy/pkg/extract"
	"github.com/devsy-org/devsy/pkg/log"
)

// releaseSource describes how to fetch a tool binary from a GitHub release.
type releaseSource struct {
	// version is the release tag without the leading "v" (e.g. "3.5.1").
	version string
	// assetName builds the release asset filename for the given GOOS/GOARCH.
	assetName func(goos, goarch, version string) (string, error)
	// downloadURL builds the download URL for a resolved asset filename.
	downloadURL func(version, asset string) string
	// checksums maps asset filename to its expected lowercase-hex SHA-256. The
	// download is rejected if its digest is absent or does not match, pinning
	// the exact bytes we install (defense against a compromised release host).
	checksums map[string]string
	// binaryInArchive is the name of the executable within the extracted
	// archive (it may sit under a versioned subdirectory).
	binaryInArchive string
	// installDir is the directory the binary is placed into; it must be on PATH.
	// Empty defaults to the platform's SystemBinDir (see pkg/config).
	installDir string
}

// gitLFSRelease pins a git-lfs release used when no package manager is present.
// git-lfs publishes per-OS/arch archives (.tar.gz on linux, .zip on darwin and
// windows) each containing a single git-lfs binary (git-lfs.exe on windows).
var gitLFSRelease = releaseSource{
	version:         "3.5.1",
	binaryInArchive: binGitLFS,
	assetName: func(goos, goarch, version string) (string, error) {
		ext, ok := map[string]string{
			osLinux:   "tar.gz",
			osDarwin:  "zip",
			osWindows: "zip",
		}[goos]
		if !ok {
			return "", fmt.Errorf("unsupported OS %q for git-lfs release download", goos)
		}
		switch goarch {
		case "amd64", "arm64":
		default:
			return "", fmt.Errorf(
				"unsupported architecture %q for git-lfs release download",
				goarch,
			)
		}
		return fmt.Sprintf("git-lfs-%s-%s-v%s.%s", goos, goarch, version, ext), nil
	},
	downloadURL: func(version, asset string) string {
		return fmt.Sprintf(
			"https://github.com/git-lfs/git-lfs/releases/download/v%s/%s",
			version, asset,
		)
	},
	// SHA-256 digests from git-lfs v3.5.1 sha256sums.asc for the OS/arch pairs
	// assetName can resolve. Update alongside version.
	checksums: map[string]string{
		"git-lfs-linux-amd64-v3.5.1.tar.gz": "6f28eb19faa7a968882dca190d92adc82493378b933958d67ceaeb9ebe4d731e",
		"git-lfs-linux-arm64-v3.5.1.tar.gz": "4f8700aacaa0fd26ae5300fb0996aed14d1fd0ce1a63eb690629c132ff5163a9",
		"git-lfs-darwin-amd64-v3.5.1.zip":   "23f6c768e22a33dcbb57d6cb67d318dc0edc2b16ac04b15faa803a74a31e8c42",
		"git-lfs-darwin-arm64-v3.5.1.zip":   "1570833e5011290dff12a18416580bfed576bc797b7b521122916e09adf4622d",
		"git-lfs-windows-amd64-v3.5.1.zip":  "94435072f6b3a6f9064b277760c8340e432b5ede0db8205d369468b9be52c6b6",
		"git-lfs-windows-arm64-v3.5.1.zip":  "54fb4a04a5597ebdae83b2873adb363c2e2b7022b8b2ce813cc0f198c12f8a61",
	},
}

// install downloads the release asset for the current platform, extracts the
// binary, and places it on PATH under installDir.
func (s *releaseSource) install(ctx context.Context, binary string) error {
	asset, err := s.assetName(runtime.GOOS, runtime.GOARCH, s.version)
	if err != nil {
		return err
	}
	wantSum, ok := s.checksums[asset]
	if !ok {
		return fmt.Errorf("no pinned checksum for %s release asset %q", binary, asset)
	}

	installDir := s.installDir
	if installDir == "" {
		installDir, err = config.DefaultPathManager().SystemBinDir()
		if err != nil {
			return fmt.Errorf("resolve install dir for %s: %w", binary, err)
		}
	}

	req := fetchRequest{
		binary:  binary,
		url:     s.downloadURL(s.version, asset),
		wantSum: wantSum,
		// git-lfs on windows ships as git-lfs.exe inside the archive.
		execName: executableName(s.binaryInArchive, runtime.GOOS),
	}
	src, cleanup, err := s.fetchBinary(ctx, req)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := os.MkdirAll(installDir, 0o750); err != nil {
		return fmt.Errorf("create install dir %q: %w", installDir, err)
	}
	dst := filepath.Join(installDir, req.execName)
	if err := moveExecutable(src, dst); err != nil {
		return fmt.Errorf("install %s to %q: %w", binary, dst, err)
	}
	return nil
}

// fetchRequest describes a single release-asset download and extraction.
type fetchRequest struct {
	binary   string // human-facing tool name for logs/errors
	url      string // resolved asset download URL
	wantSum  string // pinned lowercase-hex SHA-256 of the asset
	execName string // executable filename to locate inside the archive
}

// executableName returns the platform-specific executable filename, appending
// the .exe extension on windows.
func executableName(base, goos string) string {
	if goos == osWindows {
		return base + ".exe"
	}
	return base
}

// fetchBinary downloads the release asset, verifies it against the pinned
// SHA-256, then extracts it into a temp dir and returns the path to the
// extracted binary plus a cleanup func for the temp dir.
func (s *releaseSource) fetchBinary(
	ctx context.Context,
	req fetchRequest,
) (path string, cleanup func(), err error) {
	log.Infof("downloading %s %s release from %s", req.binary, s.version, req.url)
	body, err := download.File(ctx, req.url)
	if err != nil {
		return "", nil, fmt.Errorf("download %s release: %w", req.binary, err)
	}
	defer func() { _ = body.Close() }()

	archive, err := readVerified(body, req.wantSum)
	if err != nil {
		return "", nil, fmt.Errorf("verify %s release: %w", req.binary, err)
	}

	tmpDir, err := os.MkdirTemp("", "git-lfs-release-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(tmpDir) }

	if err := extract.Extract(archive, tmpDir); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("extract %s release: %w", req.binary, err)
	}

	src, err := findBinary(tmpDir, req.execName)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return src, cleanup, nil
}

// readVerified reads r fully and returns a reader over its bytes only if the
// content's SHA-256 matches wantSum (lowercase hex).
func readVerified(r io.Reader, wantSum string) (io.Reader, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != wantSum {
		return nil, fmt.Errorf("checksum mismatch: got %s, want %s", got, wantSum)
	}
	return bytes.NewReader(data), nil
}

// findBinary locates a named executable anywhere under root (git-lfs tarballs
// place it in a versioned subdirectory).
func findBinary(root, name string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && d.Name() == name {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("binary %q not found in downloaded archive", name)
	}
	return found, nil
}

// moveExecutable copies src to dst with executable permissions. Both paths are
// internally constructed (temp extraction dir and the configured install dir),
// not user input.
func moveExecutable(src, dst string) error {
	data, err := os.ReadFile(src) // #nosec G304 -- src is within our temp extraction dir
	if err != nil {
		return err
	}
	// #nosec G306,G703 -- dst is internally constructed; an executable must be world-executable
	return os.WriteFile(dst, data, 0o755)
}
