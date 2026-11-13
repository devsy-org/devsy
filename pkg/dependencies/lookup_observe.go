package dependencies

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"runtime"
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
	Script    bool      `json:"script"`
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
		Launcher: e.indirectLauncher(path), Script: scriptPath(path),
	}
	observation, err := e.inspectFile(ctx, path)
	if lookupCanceled(err) {
		return ExecutableCandidate{}, err
	}
	candidate.IsSymlink = observation.symlink
	if err != nil {
		candidate.State = fileErrorState(err, candidate.IsSymlink)
		if candidate.State == FileDeferred {
			candidate.ReasonCode = LookupInspectionDeferred
		}
		return candidate, nil
	}
	candidate.ResolvedPath = observation.resolved
	candidate.Script = candidate.Script || scriptPath(observation.resolved)
	candidate.Launcher = candidate.Launcher || e.indirectLauncher(observation.resolved)
	info := observation.info
	candidate.recordFile(info)
	return candidate, nil
}

func (c *ExecutableCandidate) recordFile(info fs.FileInfo) {
	c.State = observedFileState(c.LexicalPath, info)
	// Windows observations already contain IDs captured from the open handle.
	if !c.Launcher && info.Mode().IsRegular() && os.SameFile(info, info) {
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
	if errors.Is(err, fs.ErrNotExist) {
		if symlink {
			return FileBrokenLink
		}
		return FileMissing
	}
	return FileInaccessible
}

func observedFileState(path string, info fs.FileInfo) FileState {
	if !info.Mode().IsRegular() {
		return FileNonregular
	}
	if !executableFile(path, info) {
		return FileNonexecutable
	}
	return FileExecutable
}

type lookupFile struct {
	info     fs.FileInfo
	resolved string
	symlink  bool
}

var errLookupDeferred = errors.New("passive filesystem inspection deferred by policy")

func deferredCandidate(path string, origin LookupOrigin) ExecutableCandidate {
	return ExecutableCandidate{
		Origin: origin, LexicalPath: path, Platform: runtime.GOOS + "/" + runtime.GOARCH,
		State: FileDeferred, ReasonCode: LookupInspectionDeferred, Script: scriptPath(path),
	}
}

func lookupCanceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
