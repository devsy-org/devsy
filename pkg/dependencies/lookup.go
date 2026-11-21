package dependencies

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type LookupOrigin string

const (
	LookupExplicit           LookupOrigin = "explicit_option"
	LookupExplicitEnv        LookupOrigin = "explicit_env"
	LookupPATH               LookupOrigin = "external_path"
	LookupReviewed           LookupOrigin = "well_known_path"
	LookupMissing                         = "dependency_missing"
	LookupInvalidPath                     = "dependency_path_invalid"
	LookupIndirectLauncher                = "dependency_indirect_launcher"
	LookupInspectionDeferred              = "dependency_inspection_deferred"
)

// LookupRequest contains a command name or an explicit invocation path.
// Explicit retains user intent even for an empty value or a manifest default.
// ReviewedLocations are ordered, complete absolute executable paths reviewed by
// the caller for this provider/host. They are not directories to scan.
type LookupRequest struct {
	Command  string
	Explicit bool
	// ExplicitOrigin defaults to explicit_option and may also be explicit_env.
	ExplicitOrigin    LookupOrigin
	ReviewedLocations []string
}

// LookupResult is filesystem evidence, not a selection or a full Survey report.
// A deferred candidate retains lexical evidence without an executable claim.
// An empty ReasonCode means at least one executable file was observed; it says
// nothing about version, compatibility, reachability, native format, or trust.
type LookupResult struct {
	Candidates []ExecutableCandidate `json:"candidates"`
	ReasonCode string                `json:"reasonCode,omitempty"`
	Executed   bool                  `json:"executed"`
}

// LookupExecutable performs only metadata reads on the current native OS.
// Explicit command names use only snapshot PATH, stopping at its first executable
// observation (including a shim), or at a deferred inspection. Explicit paths
// never fall back. Automatic discovery keeps ordered observations from PATH
// before reviewed locations.
func (e LookupEnvironment) LookupExecutable(
	ctx context.Context,
	request LookupRequest,
) (LookupResult, error) {
	if !filepath.IsAbs(e.cwd) {
		return LookupResult{}, errors.New("lookup requires an environment snapshot")
	}
	if err := ctx.Err(); err != nil {
		return LookupResult{}, err
	}
	if request.Explicit {
		return e.lookupExplicit(ctx, request)
	}
	if err := validateAutomaticLookup(request); err != nil {
		return LookupResult{}, err
	}
	result, err := e.searchPATH(ctx, request.Command, LookupPATH)
	if err != nil {
		return LookupResult{}, err
	}
	for _, path := range request.ReviewedLocations {
		candidate, err := e.observe(ctx, path, LookupReviewed)
		if err != nil {
			return LookupResult{}, err
		}
		result.append(candidate)
	}
	result.finish(false)
	return result, nil
}

func (e LookupEnvironment) lookupExplicit(
	ctx context.Context,
	request LookupRequest,
) (LookupResult, error) {
	origin, err := request.explicitOrigin()
	if err != nil {
		return LookupResult{}, err
	}
	command := request.Command
	invalid := LookupResult{ReasonCode: LookupInvalidPath}
	if command == "" || strings.ContainsRune(command, 0) {
		return invalid, nil
	}
	if commandName(command) {
		result, err := e.searchPATH(ctx, command, origin)
		if err != nil {
			return LookupResult{}, err
		}
		result.finish(true)
		return result, err
	}
	path, valid := e.absolute(command)
	if !valid {
		return invalid, nil
	}
	return e.lookupExplicitPath(ctx, path, origin)
}

func (e LookupEnvironment) lookupExplicitPath(
	ctx context.Context,
	path string,
	origin LookupOrigin,
) (LookupResult, error) {
	result := LookupResult{}
	names := e.executableNames(path)
	if len(names) == 0 || names[0] != path {
		names = append([]string{path}, names...)
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return LookupResult{}, err
		}
		candidate, err := e.observe(ctx, name, origin)
		if err != nil {
			return LookupResult{}, err
		}
		result.Candidates = append(result.Candidates, candidate)
		if candidate.State == FileExecutable || candidate.State == FileDeferred {
			break
		}
	}
	result.finish(true)
	return result, nil
}

func (e LookupEnvironment) searchPATH(
	ctx context.Context,
	command string,
	origin LookupOrigin,
) (LookupResult, error) {
	result := LookupResult{}
	directories, err := e.pathDirectories(ctx)
	if err != nil {
		return LookupResult{}, err
	}
	for _, directory := range directories {
		for _, name := range e.executableNames(command) {
			candidate, err := e.observeDirectory(ctx, directory, name, origin)
			if err != nil {
				return LookupResult{}, err
			}
			result.append(candidate)
			if origin != LookupPATH &&
				(candidate.State == FileExecutable || candidate.State == FileDeferred) {
				return result, nil
			}
		}
	}
	return result, nil
}

func (e LookupEnvironment) observeDirectory(
	ctx context.Context,
	directory lookupDirectory,
	name string,
	origin LookupOrigin,
) (ExecutableCandidate, error) {
	if err := ctx.Err(); err != nil {
		return ExecutableCandidate{}, err
	}
	path := directory.path + string(filepath.Separator) + name
	if directory.deferred {
		return deferredCandidate(path, origin), nil
	}
	return e.observe(ctx, path, origin)
}

func (r LookupRequest) explicitOrigin() (LookupOrigin, error) {
	switch r.ExplicitOrigin {
	case "", LookupExplicit:
		return LookupExplicit, nil
	case LookupExplicitEnv:
		return LookupExplicitEnv, nil
	default:
		return "", errors.New("invalid explicit lookup origin")
	}
}

type lookupDirectory struct {
	path     string
	deferred bool
}

func (e LookupEnvironment) pathDirectories(ctx context.Context) ([]lookupDirectory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, _ := e.Value("PATH")
	var directories []lookupDirectory
	for _, directory := range filepath.SplitList(path) {
		if filepath.IsAbs(directory) {
			directories = append(directories, lookupDirectory{path: directory})
		}
	}
	if len(directories) == 0 {
		return nil, ctx.Err()
	}
	return e.excludeWorkingDirectory(ctx, directories)
}

func (e LookupEnvironment) excludeWorkingDirectory(
	ctx context.Context,
	directories []lookupDirectory,
) ([]lookupDirectory, error) {
	// Compare actual directory identity without cleaning away Unix link/.. traversal.
	cwd, cwdErr := e.inspectFile(ctx, e.cwd)
	if lookupCanceled(cwdErr) {
		return nil, cwdErr
	}
	filtered := directories[:0]
	for _, entry := range directories {
		entry.deferred = cwdErr != nil
		if cwdErr == nil {
			observed, err := e.inspectFile(ctx, entry.path)
			if lookupCanceled(err) {
				return nil, err
			}
			entry.deferred = errors.Is(err, errLookupDeferred)
			if err == nil && os.SameFile(cwd.info, observed.info) {
				continue
			}
		}
		filtered = append(filtered, entry)
	}
	directories = filtered
	return directories, ctx.Err()
}

func (r *LookupResult) append(candidate ExecutableCandidate) {
	if candidate.State == FileMissing {
		return
	}
	for _, existing := range r.Candidates {
		if existing.LexicalPath == candidate.LexicalPath {
			return
		}
	}
	r.Candidates = append(r.Candidates, candidate)
}

func (r *LookupResult) finish(explicit bool) {
	r.ReasonCode = LookupMissing
	if explicit {
		r.ReasonCode = LookupInvalidPath
	}
	for _, candidate := range r.Candidates {
		if candidate.State == FileExecutable || candidate.State == FileDeferred {
			r.ReasonCode = candidate.ReasonCode
			return
		}
	}
}

func commandName(command string) bool {
	return command != "" && command != "." && command != ".." &&
		!strings.ContainsRune(command, 0) && !invocationPath(command)
}

func validateAutomaticLookup(request LookupRequest) error {
	if request.ExplicitOrigin != "" {
		return errors.New("automatic lookup cannot have an explicit origin")
	}
	if !commandName(request.Command) {
		return errors.New("automatic lookup requires a command name")
	}
	for _, path := range request.ReviewedLocations {
		if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
			return errors.New("reviewed executable location must be absolute")
		}
	}
	return nil
}
