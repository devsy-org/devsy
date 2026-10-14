package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// dockerCLIURL and dockerCLISHA256 pin the reviewed Windows Docker CLI. Change
	// both together, only after verifying the new archive out of band.
	dockerCLIURL    = "https://download.docker.com/win/static/stable/x86_64/docker-29.9.0.zip"
	dockerCLISHA256 = "e3ff5d2e4cd1ad7e1c9bcb76f0df28a84853c43416a915323f08659d8487ad83"

	dockerExeName = "docker.exe"

	stageDockerCLI = "docker-cli"

	maxDockerZipBytes   = 128 << 20
	maxZipEntries       = 1000
	maxZipUnpackedBytes = 512 << 20

	extractedDirPerm  fs.FileMode = 0o755
	extractedFilePerm fs.FileMode = 0o644
)

var zipDriveLetter = regexp.MustCompile(`^[A-Za-z]:`)

// dockerCLISource names the archive to fetch and the digest it must match.
type dockerCLISource struct {
	URL    string
	SHA256 string
}

var pinnedDockerCLI = dockerCLISource{URL: dockerCLIURL, SHA256: dockerCLISHA256}

// fetchDockerCLI downloads the archive into memory, verifies its SHA-256 and
// only then extracts it below dest. Nothing is written to disk before the
// digest matches, so an unverified binary can never become runnable. It returns
// the directory that holds docker.exe.
func fetchDockerCLI(
	ctx context.Context,
	deps *Dependencies,
	src dockerCLISource,
	dest string,
) (string, error) {
	data, err := downloadVerified(ctx, deps, src)
	if err != nil {
		return "", err
	}
	binDir, err := extractZip(deps.Files, data, dest)
	if err != nil {
		return "", newStageError(CodeDockerWSLInstall, stageDockerCLI,
			fmt.Errorf("extract docker CLI archive: %w", err))
	}
	return binDir, nil
}

func downloadVerified(
	ctx context.Context,
	deps *Dependencies,
	src dockerCLISource,
) ([]byte, error) {
	var buf bytes.Buffer
	sum := sha256.New()
	if err := deps.HTTP.Download(
		ctx,
		src.URL,
		io.MultiWriter(&buf, sum),
		maxDockerZipBytes,
	); err != nil {
		return nil, newStageError(CodeDockerWSLInstall, stageDockerCLI, err)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != strings.ToLower(src.SHA256) {
		return nil, newStageError(CodeDockerCLIChecksum, stageDockerCLI,
			fmt.Errorf("SHA-256 mismatch for %s: want %s, got %s", src.URL, src.SHA256, got))
	}
	return buf.Bytes(), nil
}

// zipEntry is a validated archive member and its destination.
type zipEntry struct {
	file   *zip.File
	name   string // slash-separated, relative, cleaned
	target string
}

// extractZip validates every entry before writing the first one, then writes
// regular files only. It returns the directory containing docker.exe.
func extractZip(files FileStore, data []byte, dest string) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open archive: %w", err)
	}
	entries, err := planZipEntries(zr, dest)
	if err != nil {
		return "", err
	}
	exe, err := findDockerExe(entries)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if err := writeZipEntry(files, e); err != nil {
			return "", err
		}
	}
	if _, err := files.Stat(exe); err != nil {
		return "", fmt.Errorf("%s missing after extraction: %w", dockerExeName, err)
	}
	return filepath.Dir(exe), nil
}

func planZipEntries(zr *zip.Reader, dest string) ([]zipEntry, error) {
	if len(zr.File) == 0 || len(zr.File) > maxZipEntries {
		return nil, fmt.Errorf("archive has %d entries, want 1..%d", len(zr.File), maxZipEntries)
	}
	var (
		entries []zipEntry
		total   uint64
		seen    = map[string]string{}
	)
	for _, f := range zr.File {
		name, err := cleanZipName(f.Name)
		if err != nil {
			return nil, err
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if err := checkZipFile(f, name, seen); err != nil {
			return nil, err
		}
		if total += f.UncompressedSize64; total > maxZipUnpackedBytes {
			return nil, fmt.Errorf("archive unpacks past %d bytes", int64(maxZipUnpackedBytes))
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		entries = append(entries, zipEntry{file: f, name: name, target: target})
	}
	return entries, checkZipConflicts(seen)
}

// cleanZipName rejects absolute, drive-qualified, backslash, stream and
// traversal names; it returns the slash-separated relative name.
func cleanZipName(raw string) (string, error) {
	name := strings.TrimSuffix(raw, "/")
	if !safeZipName(name) {
		return "", fmt.Errorf("unsafe archive entry name %q", raw)
	}
	return name, nil
}

func safeZipName(name string) bool {
	return name != "" && !strings.ContainsAny(name, "\\\x00") && path.Clean(name) == name &&
		!strings.HasPrefix(name, "/") && !zipDriveLetter.MatchString(name) && safeZipSegments(name)
}

func safeZipSegments(name string) bool {
	for part := range strings.SplitSeq(name, "/") {
		if part == ".." || part == "." || strings.Contains(part, ":") {
			return false
		}
	}
	return true
}

// checkZipFile accepts regular files only and tracks case-folded names so two
// entries can never collide on a case-insensitive filesystem.
func checkZipFile(f *zip.File, name string, seen map[string]string) error {
	if !f.Mode().IsRegular() {
		return fmt.Errorf("archive entry %q is not a regular file", f.Name)
	}
	key := strings.ToLower(name)
	if prev, dup := seen[key]; dup {
		return fmt.Errorf("duplicate archive entry %q conflicts with %q", f.Name, prev)
	}
	seen[key] = f.Name
	return nil
}

// checkZipConflicts rejects a file that is also used as a directory.
func checkZipConflicts(seen map[string]string) error {
	for key := range seen {
		for dir := path.Dir(key); dir != "."; dir = path.Dir(dir) {
			if prev, isFile := seen[dir]; isFile {
				return fmt.Errorf("archive entry %q is both a file and a directory", prev)
			}
		}
	}
	return nil
}

func findDockerExe(entries []zipEntry) (string, error) {
	var found []string
	for _, e := range entries {
		if strings.EqualFold(path.Base(e.name), dockerExeName) {
			found = append(found, e.target)
		}
	}
	if len(found) != 1 {
		return "", fmt.Errorf(
			"archive must contain exactly one %s, found %d",
			dockerExeName,
			len(found),
		)
	}
	return found[0], nil
}

func writeZipEntry(files FileStore, e zipEntry) error {
	rc, err := e.file.Open()
	if err != nil {
		return fmt.Errorf("open %q: %w", e.name, err)
	}
	defer func() { _ = rc.Close() }()
	declared := int64(e.file.UncompressedSize64)
	data, err := io.ReadAll(io.LimitReader(rc, declared+1))
	if err != nil {
		return fmt.Errorf("read %q: %w", e.name, err)
	}
	if int64(len(data)) != declared {
		return fmt.Errorf("archive entry %q size differs from its header", e.name)
	}
	if err := files.MkdirAll(filepath.Dir(e.target), extractedDirPerm); err != nil {
		return fmt.Errorf("create directory for %q: %w", e.name, err)
	}
	if err := files.WriteFile(e.target, data, extractedFilePerm); err != nil {
		return fmt.Errorf("write %q: %w", e.name, err)
	}
	return nil
}
