package extract

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/moby/patternmatcher"
)

// TarOptions configures WriteTarWithOptions.
type TarOptions struct {
	// Compress gzips the archive.
	Compress bool
	// Excludes are patterns relative to the archived folder, with the same
	// syntax as a .dockerignore file.
	Excludes []string
	// Matcher is an optional precompiled policy; when set it takes precedence over Excludes.
	// A matcher must belong to one transfer because matching lazily compiles its patterns.
	Matcher *patternmatcher.PatternMatcher
	// ProtectedPaths are exact slash-separated paths relative to the archive root.
	// Their contents bypass exclusions; existing path components must not be symlinks.
	ProtectedPaths []string
	// ProtectedFiles supplies approved contents for exact files. Their bodies
	// are archived from these bytes instead of reopening mutable source files.
	ProtectedFiles map[string][]byte
}

func WriteTarExclude(
	writer io.Writer,
	localPath string,
	compress bool,
	excludedPaths []string,
) error {
	return WriteTarWithOptions(
		writer,
		localPath,
		TarOptions{Compress: compress, Excludes: excludedPaths},
	)
}

// WriteTarWithOptions writes the file or the contents of the folder at
// localPath as a tar archive to writer.
func WriteTarWithOptions(writer io.Writer, localPath string, opts TarOptions) error {
	absolute, err := filepath.Abs(localPath)
	if err != nil {
		return fmt.Errorf("absolute: %w", err)
	}

	stat, err := os.Stat(absolute)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}

	basePath, relativePath := absolute, ""
	if !stat.IsDir() {
		basePath, relativePath = filepath.Dir(absolute), filepath.Base(absolute)
	}

	archiver, err := configuredArchiver(basePath, opts)
	if err != nil {
		return err
	}
	return archiver.writeArchive(writer, relativePath, opts.Compress)
}

func configuredArchiver(basePath string, opts TarOptions) (*Archiver, error) {
	matcher := opts.Matcher
	if matcher == nil && len(opts.Excludes) > 0 {
		var err error
		matcher, err = patternmatcher.New(opts.Excludes)
		if err != nil {
			return nil, fmt.Errorf("parse exclude patterns: %w", err)
		}
	}
	archiver := newArchiver(basePath, nil, matcher)
	if err := archiver.configureProtection(opts); err != nil {
		return nil, err
	}
	return archiver, nil
}

func validateProtectedFile(basePath, protected string) error {
	if err := validateProtectedPath(basePath, protected); err != nil {
		return err
	}
	info, err := os.Lstat(filepath.Join(basePath, filepath.FromSlash(protected)))
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("protected file %q is not a regular file", protected)
	}
	return nil
}

func WriteTar(writer io.Writer, localPath string, compress bool) error {
	return WriteTarExclude(writer, localPath, compress, nil)
}

// Archiver is responsible for compressing specific files and folders within a target directory.
type Archiver struct {
	basePath     string
	writer       *tar.Writer
	writtenFiles map[string]bool

	// excludes holds the exclude patterns, with .dockerignore semantics. It is
	// nil when nothing is excluded.
	excludes *patternmatcher.PatternMatcher
	// reincludes holds the components of each negation (!) pattern.
	reincludes     [][]string
	protectedPaths []string
	protectedFiles map[string][]byte
}

// NewArchiver creates a new archiver. excludedPaths are patterns relative to
// basePath, with the same syntax as a .dockerignore file.
func NewArchiver(basePath string, writer *tar.Writer, excludedPaths []string) (*Archiver, error) {
	var excludes *patternmatcher.PatternMatcher
	if len(excludedPaths) > 0 {
		var err error
		excludes, err = patternmatcher.New(excludedPaths)
		if err != nil {
			return nil, fmt.Errorf("parse exclude patterns: %w", err)
		}
	}

	return newArchiver(basePath, writer, excludes), nil
}

func newArchiver(
	basePath string,
	writer *tar.Writer,
	excludes *patternmatcher.PatternMatcher,
) *Archiver {
	return &Archiver{
		basePath:     basePath,
		writer:       writer,
		writtenFiles: map[string]bool{},
		excludes:     excludes,
		reincludes:   reincludePatterns(excludes),
	}
}

// reincludePatterns returns the components of each negation pattern.
func reincludePatterns(excludes *patternmatcher.PatternMatcher) [][]string {
	if excludes == nil || !excludes.Exclusions() {
		return nil
	}

	var reincludes [][]string
	for _, p := range excludes.Patterns() {
		if p.Exclusion() {
			reincludes = append(reincludes, strings.Split(filepath.ToSlash(p.String()), "/"))
		}
	}
	return reincludes
}

// AddToArchive adds a new path to the archive.
func (a *Archiver) AddToArchive(relativePath string) error {
	relativePath = filepath.ToSlash(relativePath)
	if relativePath != "" && !validArchivePath(relativePath) {
		return fmt.Errorf(
			"invalid archive path %q: expected a local slash-separated path",
			relativePath,
		)
	}
	return a.addToArchive(relativePath, patternmatcher.MatchInfo{})
}

func (a *Archiver) configureProtection(opts TarOptions) error {
	for _, protected := range opts.ProtectedPaths {
		if err := validateProtectedPath(a.basePath, protected); err != nil {
			return err
		}
	}
	a.protectedPaths = append([]string(nil), opts.ProtectedPaths...)
	for protected := range opts.ProtectedFiles {
		if err := validateProtectedFile(a.basePath, protected); err != nil {
			return err
		}
		a.protectedPaths = append(a.protectedPaths, protected)
	}
	a.protectedFiles = opts.ProtectedFiles
	return nil
}

func (a *Archiver) writeArchive(writer io.Writer, relativePath string, compress bool) error {
	gw := writer
	var gzipWriter *gzip.Writer
	if compress {
		gzipWriter = gzip.NewWriter(writer)
		gw = gzipWriter
	}
	tarWriter := tar.NewWriter(gw)
	a.writer = tarWriter
	archiveErr := a.AddToArchive(relativePath)
	if archiveErr == nil {
		archiveErr = a.verifyProtectedFilesArchived()
	}
	if archiveErr != nil {
		// Finalizing either format can hide a pending transport error behind
		// a clean archive EOF. These writers own no external resources.
		return archiveErr
	}
	closeErr := tarWriter.Close()
	if gzipWriter != nil {
		closeErr = errors.Join(closeErr, gzipWriter.Close())
	}
	return closeErr
}

func (a *Archiver) verifyProtectedFilesArchived() error {
	for protected := range a.protectedFiles {
		if !a.writtenFiles[protected] {
			return fmt.Errorf("protected file %q was not archived", protected)
		}
	}
	return nil
}

// addToArchive adds a path to the archive. parentInfo holds the exclude
// results of the parent directory, or the zero value when they are unknown.
func (a *Archiver) addToArchive(relativePath string, parentInfo patternmatcher.MatchInfo) error {
	if a.writtenFiles[relativePath] {
		return nil
	}

	stat, err := os.Lstat(filepath.Join(a.basePath, filepath.FromSlash(relativePath)))
	if err != nil {
		return fmt.Errorf("stat archive path %q: %w", relativePath, err)
	}

	if approved, protected := a.protectedFiles[relativePath]; protected {
		return a.tarApprovedFile(relativePath, stat, approved)
	}
	if stat.IsDir() {
		return a.addFolder(relativePath, stat, parentInfo)
	}

	excluded, _, err := a.isExcluded(relativePath, parentInfo)
	if err != nil {
		return err
	}
	if excluded && !slices.Contains(a.protectedPaths, relativePath) {
		return nil
	}
	return a.tarFile(relativePath, stat)
}

// addFolder adds a folder and what is in it to the archive.
func (a *Archiver) addFolder(
	relativePath string,
	stat os.FileInfo,
	parentInfo patternmatcher.MatchInfo,
) error {
	if slices.Contains(a.protectedPaths, relativePath) {
		return a.tarFolderWhole(relativePath, stat)
	}

	excluded, info, err := a.isExcluded(relativePath, parentInfo)
	if err != nil {
		return err
	}

	// Skip an excluded folder without reading it, unless a negation
	// pattern could re-include something below it.
	if excluded && !a.hasProtectedDescendant(relativePath) && !a.mayReincludeBelow(relativePath) {
		return nil
	}

	return a.tarFolder(relativePath, stat, excluded, info)
}

func validArchivePath(relativePath string) bool {
	return relativePath != "." && filepath.IsLocal(relativePath) &&
		path.Clean(relativePath) == relativePath
}

func validateProtectedPath(basePath, protected string) error {
	if !validArchivePath(protected) || strings.ContainsAny(protected, `\:`) {
		return fmt.Errorf(
			"invalid protected path %q: expected a local slash-separated path",
			protected,
		)
	}
	current := basePath
	for component := range strings.SplitSeq(protected, "/") {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("validate protected path %q: %w", protected, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("protected path %q contains a symbolic link", protected)
		}
	}
	return nil
}

func (a *Archiver) hasProtectedDescendant(dir string) bool {
	for _, protected := range a.protectedPaths {
		if strings.HasPrefix(protected, dir+"/") {
			return true
		}
	}
	return false
}

// tarFolderWhole archives a folder and everything in it, ignoring the excludes.
func (a *Archiver) tarFolderWhole(relativePath string, stat os.FileInfo) error {
	whole := &Archiver{
		basePath:       a.basePath,
		writer:         a.writer,
		writtenFiles:   a.writtenFiles,
		protectedFiles: a.protectedFiles,
	}
	return whole.tarFolder(relativePath, stat, false, patternmatcher.MatchInfo{})
}

// mayReincludeBelow reports whether a negation pattern could match a path
// inside the excluded folder dir. It may return true for a pattern that turns
// out not to match, but never false for one that does.
func (a *Archiver) mayReincludeBelow(dir string) bool {
	dirComponents := strings.Split(path.Clean(filepath.ToSlash(dir)), "/")
	for _, pattern := range a.reincludes {
		if patternMayMatchBelow(pattern, dirComponents) {
			return true
		}
	}
	return false
}

// patternMayMatchBelow reports whether pattern could match a path below the
// folder dir, both given as path components.
func patternMayMatchBelow(pattern, dir []string) bool {
	// "**" matches any number of folders, so do not try to rule it out
	if strings.Contains(strings.Join(pattern, "/"), "**") {
		return true
	}
	// A pattern matching dir itself, or one of its parents, was already
	// applied to dir, which is still excluded
	if len(pattern) <= len(dir) {
		return false
	}
	for i, component := range dir {
		matched, err := path.Match(pattern[i], component)
		if err != nil || !matched {
			return err != nil
		}
	}
	return true
}

// isExcluded matches relativePath against the exclude patterns. It also
// returns the match results to pass down to the path's children.
func (a *Archiver) isExcluded(
	relativePath string,
	parentInfo patternmatcher.MatchInfo,
) (bool, patternmatcher.MatchInfo, error) {
	if a.excludes == nil {
		return false, patternmatcher.MatchInfo{}, nil
	}

	// Patterns are relative to the archive root, so the root itself never matches
	relativePath = path.Clean(filepath.ToSlash(relativePath))
	if relativePath == "." || relativePath == "/" {
		return false, patternmatcher.MatchInfo{}, nil
	}

	// With negations, the matcher can skip recording a rule on the parent
	// because an earlier rule already decided its result. That skipped rule
	// can still override a different decision on a descendant; recheck its
	// ancestors instead of inheriting incomplete per-pattern results.
	if a.excludes.Exclusions() {
		parentInfo = patternmatcher.MatchInfo{}
	}
	excluded, info, err := a.excludes.MatchesUsingParentResults(relativePath, parentInfo)
	if err != nil {
		return false, info, fmt.Errorf("match %s against exclude patterns: %w", relativePath, err)
	}
	return excluded, info, nil
}

func (a *Archiver) tarFolder(
	target string,
	targetStat os.FileInfo,
	excluded bool,
	matchInfo patternmatcher.MatchInfo,
) error {
	filePath := filepath.Join(a.basePath, filepath.FromSlash(target))
	files, err := os.ReadDir(filePath)
	if err != nil {
		return fmt.Errorf("read archive directory %q: %w", target, err)
	}

	if len(files) == 0 && target != "" && !excluded {
		return a.tarEmptyFolder(target, targetStat)
	}

	for _, dirEntry := range files {
		if err = a.addToArchive(path.Join(target, dirEntry.Name()), matchInfo); err != nil {
			return fmt.Errorf("recursive tar %s: %w", dirEntry.Name(), err)
		}
	}

	return nil
}

func (a *Archiver) tarEmptyFolder(target string, targetStat os.FileInfo) error {
	hdr, err := tar.FileInfoHeader(targetStat, "")
	if err != nil {
		return fmt.Errorf("create tar directory header: %w", err)
	}
	// #nosec G115 -- a tar header mode always fits in an os.FileMode
	hdr.Mode = fillGo18FileTypeBits(int64(chmodTarEntry(os.FileMode(hdr.Mode))), targetStat)
	hdr.Name = target
	if err := a.writer.WriteHeader(hdr); err != nil {
		return fmt.Errorf("tar write header: %w", err)
	}
	a.writtenFiles[target] = true

	return nil
}

func (a *Archiver) tarFile(target string, targetStat os.FileInfo) error {
	var err error
	filePath := filepath.Join(a.basePath, filepath.FromSlash(target))

	// don't resolve symlinks
	linkName := ""
	if targetStat.Mode()&os.ModeSymlink == os.ModeSymlink {
		linkName, err = os.Readlink(filePath)
		if err != nil {
			return fmt.Errorf("read archive symlink %q: %w", target, err)
		}
	}

	hdr, err := tarFileHeader(target, targetStat, linkName)
	if err != nil {
		return err
	}

	if err := a.writer.WriteHeader(hdr); err != nil {
		return fmt.Errorf("tar write header: %w", err)
	}

	// nothing more to do for non-regular
	if !targetStat.Mode().IsRegular() {
		a.writtenFiles[target] = true
		return nil
	}

	return a.writeRegularFileBody(target, filePath, targetStat)
}

func (a *Archiver) tarApprovedFile(target string, stat os.FileInfo, approved []byte) error {
	if !stat.Mode().IsRegular() {
		return fmt.Errorf("protected file %q is no longer a regular file", target)
	}
	hdr, err := tarFileHeader(target, stat, "")
	if err != nil {
		return err
	}
	hdr.Size = int64(len(approved))
	if err := a.writer.WriteHeader(hdr); err != nil {
		return fmt.Errorf("tar write header: %w", err)
	}
	if _, err := a.writer.Write(approved); err != nil {
		return fmt.Errorf("tar write protected file: %w", err)
	}
	a.writtenFiles[target] = true
	return nil
}

func tarFileHeader(target string, stat os.FileInfo, linkName string) (*tar.Header, error) {
	hdr, err := tar.FileInfoHeader(stat, linkName)
	if err != nil {
		return nil, fmt.Errorf("create tar file info header: %w", err)
	}
	hdr.Name = target
	// #nosec G115 -- a tar header mode always fits in an os.FileMode.
	hdr.Mode = fillGo18FileTypeBits(int64(chmodTarEntry(os.FileMode(hdr.Mode))), stat)
	hdr.ModTime = time.Unix(stat.ModTime().Unix(), 0)
	return hdr, nil
}

func (a *Archiver) writeRegularFileBody(target, filePath string, targetStat os.FileInfo) error {
	// #nosec G304 -- path is derived from the archive being created, not external input
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open archive file %q: %w", target, err)
	}
	defer func() { _ = f.Close() }()
	copied, err := io.CopyN(a.writer, f, targetStat.Size())
	if err != nil {
		return fmt.Errorf("tar copy file: %w", err)
	} else if copied != targetStat.Size() {
		return errors.New("tar: file truncated during read")
	}

	a.writtenFiles[target] = true
	return nil
}

const (
	modeISDIR  = 0o40000  // Directory
	modeISFIFO = 0o10000  // FIFO
	modeISREG  = 0o100000 // Regular file
	modeISLNK  = 0o120000 // Symbolic link
	modeISBLK  = 0o60000  // Block special file
	modeISCHR  = 0o20000  // Character special file
	modeISSOCK = 0o140000 // Socket
)

// chmodTarEntry is used to adjust the file permissions used in tar header based
// on the platform the archival is done.
func chmodTarEntry(perm os.FileMode) os.FileMode {
	if runtime.GOOS != "windows" {
		return perm
	}

	// perm &= 0755 // this 0-ed out tar flags (like link, regular file, directory marker etc.)
	permPart := perm & os.ModePerm
	noPermPart := perm &^ os.ModePerm
	// Add the x bit: make everything +x from windows
	permPart |= 0o111
	permPart &= 0o755

	return noPermPart | permPart
}

// fillGo18FileTypeBits fills type bits which have been removed on Go 1.9 archive/tar
// https://github.com/golang/go/commit/66b5a2f
func fillGo18FileTypeBits(mode int64, fi os.FileInfo) int64 {
	fm := fi.Mode()
	switch {
	case fm.IsRegular():
		mode |= modeISREG
	case fi.IsDir():
		mode |= modeISDIR
	case fm&os.ModeSymlink != 0:
		mode |= modeISLNK
	case fm&os.ModeDevice != 0:
		if fm&os.ModeCharDevice != 0 {
			mode |= modeISCHR
		} else {
			mode |= modeISBLK
		}
	case fm&os.ModeNamedPipe != 0:
		mode |= modeISFIFO
	case fm&os.ModeSocket != 0:
		mode |= modeISSOCK
	}
	return mode
}
