package dependencies

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

// Limits keep a hostile PATH or PATHEXT from turning a lookup into unbounded work.
// A real PATH has a few dozen entries and a real PATHEXT a handful.
const (
	maxPathEntries    = 512
	maxExecutableName = 32
)

// Lookup collects filesystem evidence for one dependency command. It never runs
// a program and never selects a candidate. An explicit request names one path,
// resolved against the snapshot directory. An automatic request searches the
// snapshot PATH in order and then the reviewed locations as given. Where the
// platform ignores case, paths that differ only in case count as one candidate.
func (e LookupEnvironment) Lookup(
	ctx context.Context,
	request LookupRequest,
) (LookupResult, error) {
	if err := ctx.Err(); err != nil {
		return LookupResult{}, err
	}
	if request.Explicit {
		return e.lookupExplicit(ctx, request)
	}
	return e.lookupAutomatic(ctx, request)
}

func (e LookupEnvironment) lookupExplicit(
	ctx context.Context,
	request LookupRequest,
) (LookupResult, error) {
	origin := request.ExplicitOrigin
	if origin == "" {
		origin = LookupExplicit
	}
	if origin != LookupExplicit && origin != LookupExplicitEnv {
		return LookupResult{}, errors.New("explicit lookup has an unknown origin")
	}
	if len(request.ReviewedLocations) > 0 {
		return LookupResult{}, errors.New("explicit lookup takes no reviewed locations")
	}
	var result LookupResult
	if path, ok := e.absolute(request.Command); ok {
		if err := e.collect(ctx, &result, path, origin); err != nil {
			return LookupResult{}, err
		}
	}
	result.finish(true)
	return result, nil
}

func (e LookupEnvironment) lookupAutomatic(
	ctx context.Context,
	request LookupRequest,
) (LookupResult, error) {
	if err := e.validateAutomaticLookup(request); err != nil {
		return LookupResult{}, err
	}
	var result LookupResult
	if err := e.searchPath(ctx, &result, request.Command); err != nil {
		return LookupResult{}, err
	}
	for _, path := range request.ReviewedLocations {
		if err := e.collect(ctx, &result, path, LookupReviewed); err != nil {
			return LookupResult{}, err
		}
	}
	result.finish(false)
	return result, nil
}

func (e LookupEnvironment) searchPath(
	ctx context.Context,
	result *LookupResult,
	command string,
) error {
	directories, err := e.searchDirectories()
	if err != nil {
		return err
	}
	names := e.executableNames(command)
	if len(names) > maxExecutableName {
		return errors.New("PATHEXT lists too many extensions")
	}
	for _, directory := range directories {
		for _, name := range names {
			if err := e.collect(ctx, result, directory+e.separator()+name, LookupPATH); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e LookupEnvironment) collect(
	ctx context.Context,
	result *LookupResult,
	path string,
	origin LookupOrigin,
) error {
	candidate, err := e.observe(ctx, path, origin)
	if err != nil {
		return err
	}
	result.append(e, candidate)
	return nil
}

// searchDirectories returns the usable PATH entries in order. An empty entry,
// ".", and any entry that is not a local absolute path is skipped, so the
// working directory is never searched. Entries are used as written. A PATH
// with more than maxPathEntries entries is refused instead of searched in part.
func (e LookupEnvironment) searchDirectories() ([]string, error) {
	entries := e.pathEntries()
	if len(entries) > maxPathEntries {
		return nil, errors.New("PATH lists too many entries")
	}
	var directories []string
	for _, entry := range entries {
		if !e.localAbsolute(entry) {
			continue
		}
		directories = append(directories, strings.TrimRight(entry, e.separators()))
	}
	return directories, nil
}

// pathEntries splits the snapshot PATH with the list separator of the snapshot
// platform, not of the build platform. A Windows snapshot always splits on ";"
// and never honors quotes, so every build reads the same entries: a quoted entry
// is not an absolute path and is skipped.
func (e LookupEnvironment) pathEntries() []string {
	value, _ := e.Value("PATH")
	if e.windows {
		return strings.Split(value, ";")
	}
	return filepath.SplitList(value)
}
