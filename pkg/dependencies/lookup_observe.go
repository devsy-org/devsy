package dependencies

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"runtime"
	"syscall"
)

type FileState string

// FileExecutable means observed execute bits on Unix or an invocation suffix on
// Windows. It is not an access check or a native executable format check.
const (
	FileExecutable    FileState = "executable_file"
	FileMissing       FileState = "missing"
	FileBrokenLink    FileState = "broken_link"
	FileNonregular    FileState = "nonregular"
	FileNonexecutable FileState = "nonexecutable"
	FileInaccessible  FileState = "inaccessible"
	FileDeferred      FileState = "inspection_deferred"
)

type ExecutableCandidate struct {
	Origin       LookupOrigin `json:"sourceKind"`
	LexicalPath  string       `json:"lexicalPath"`
	ResolvedPath string       `json:"resolvedPath,omitempty"`
	// IsSymlink includes link traversal through parent directories.
	IsSymlink bool      `json:"isSymlink"`
	State     FileState `json:"fileState"`
	Launcher  bool      `json:"indirectLauncher"`
	// Script marks .cmd, .bat and .ps1 files. A caller must not pass untrusted
	// arguments to one, because cmd.exe parses the argument text again.
	Script bool `json:"script"`
	// Platform identifies the observer, not an inspected binary architecture.
	Platform   string `json:"platform"`
	ReasonCode string `json:"reasonCode,omitempty"`
	Executed   bool   `json:"executed"`
	// Identity is request-local evidence, intentionally not a serializable ID.
	Identity *FileObservation `json:"-"`
}

// FileObservation compares observed filesystem identity, not content integrity.
// Metadata reads are not atomic with later invocation. Reobserve before use;
// SameFile does not authorize execution, ownership, or deletion.
type FileObservation struct {
	info fs.FileInfo
}

func (o *FileObservation) SameFile(other *FileObservation) bool {
	return o != nil && other != nil && os.SameFile(o.info, other.info)
}

func (e LookupEnvironment) observe(
	ctx context.Context,
	path string,
	origin LookupOrigin,
) (ExecutableCandidate, error) {
	candidate := ExecutableCandidate{
		Origin: origin, LexicalPath: path, Platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
	if !e.localAbsolute(path) {
		candidate.State, candidate.ReasonCode = FileInaccessible, LookupInvalidPath
		return candidate, nil
	}
	candidate.Launcher, candidate.Script = e.indirectLauncher(path), e.scriptPath(path)
	observation, err := e.inspectFile(ctx, path)
	if lookupCanceled(err) {
		return ExecutableCandidate{}, err
	}
	if err == nil && observation.info == nil {
		err = errLookupNoInfo
	}
	candidate.IsSymlink = observation.symlink
	if err != nil {
		candidate.State = fileErrorState(err, candidate.IsSymlink)
		candidate.ReasonCode = errorReason(candidate.State)
		return candidate, nil
	}
	candidate.ResolvedPath = observation.resolved
	candidate.Script = candidate.Script || e.scriptPath(observation.resolved)
	candidate.Launcher = candidate.Launcher || e.indirectLauncher(observation.resolved)
	candidate.recordFile(e.observedFileState(path, observation), observation.info)
	return candidate, nil
}

// recordFile keeps the inspector's file info as the identity. Inspectors must
// build that info from the file they opened, not from a second path lookup.
func (c *ExecutableCandidate) recordFile(state FileState, info fs.FileInfo) {
	c.State = state
	if !c.Launcher && info.Mode().IsRegular() {
		c.Identity = &FileObservation{info: info}
	}
	if c.Launcher {
		c.ReasonCode = LookupIndirectLauncher
	}
}

func fileErrorState(err error, symlink bool) FileState {
	if errors.Is(err, errLookupDeferred) {
		return FileDeferred
	}
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		if symlink {
			return FileBrokenLink
		}
		return FileMissing
	}
	return FileInaccessible
}

func errorReason(state FileState) string {
	switch state {
	case FileDeferred:
		return LookupInspectionDeferred
	case FileInaccessible:
		return LookupInaccessible
	}
	return ""
}

// observedFileState requires an invocation suffix on the lexical name and, for
// a link, on its target, so a link named tool.exe that points at data.txt is not
// executable.
func (e LookupEnvironment) observedFileState(path string, file lookupFile) FileState {
	if !file.info.Mode().IsRegular() {
		return FileNonregular
	}
	if !e.executableFile(path, file.info) ||
		file.resolved != "" && !e.executableFile(file.resolved, file.info) {
		return FileNonexecutable
	}
	return FileExecutable
}

type lookupFile struct {
	info     fs.FileInfo
	resolved string
	symlink  bool
}

var (
	errLookupDeferred = errors.New("passive filesystem inspection deferred by policy")
	errLookupNoInfo   = errors.New("inspector returned no file information")
)

func (e LookupEnvironment) deferredCandidate(path string, origin LookupOrigin) ExecutableCandidate {
	return ExecutableCandidate{
		Origin: origin, LexicalPath: path, Platform: runtime.GOOS + "/" + runtime.GOARCH,
		State: FileDeferred, ReasonCode: LookupInspectionDeferred, Script: e.scriptPath(path),
	}
}

func lookupCanceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
