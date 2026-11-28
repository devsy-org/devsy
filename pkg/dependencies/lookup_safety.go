package dependencies

import (
	"context"
	"io/fs"
	"strings"
	"unicode/utf16"
)

// windowsLookupMetadata is the native boundary of the Windows walk. Each open
// is either a validated native disk root or a single child of a pinned handle.
// Implementations must open the reparse point itself and never follow it, and
// stat must read the handle it is given, not the path the handle was opened by.
type windowsLookupMetadata interface {
	driveTarget(drive string) (string, error)
	open(request windowsLookupOpen) (uintptr, error)
	stat(handle uintptr) (info fs.FileInfo, reparse bool, err error)
	close(handle uintptr)
}

// windowsLookupOpen asks for one child of a pinned parent. A zero parent means
// the volume root. The directory flag is set for every entry that must be a
// directory so the kernel refuses a file or device there.
type windowsLookupOpen struct {
	parent    uintptr
	name      string
	directory bool
}

func inspectWindowsLookup(
	ctx context.Context,
	path string,
	metadata windowsLookupMetadata,
) (lookupFile, error) {
	if err := ctx.Err(); err != nil {
		return lookupFile{}, err
	}
	drive, parts, valid := windowsLookupPath(path)
	if !valid {
		return lookupFile{}, errLookupDeferred
	}
	target, err := metadata.driveTarget(drive)
	if canceled := ctx.Err(); canceled != nil {
		return lookupFile{}, canceled
	}
	if err != nil || !localWindowsVolume(target) {
		return lookupFile{}, errLookupDeferred
	}
	handle, err := openWindowsLookup(ctx, windowsLookupOpen{
		name: target + `\`, directory: true,
	}, metadata)
	if err != nil {
		return lookupFile{}, err
	}
	observed, err := walkWindowsLookup(ctx, handle, parts, metadata)
	if err == nil {
		observed.resolved = drive + `\` + strings.Join(parts, `\`)
	}
	return observed, err
}

func walkWindowsLookup(
	ctx context.Context,
	handle uintptr,
	parts []string,
	metadata windowsLookupMetadata,
) (lookupFile, error) {
	defer func() { metadata.close(handle) }()
	for {
		info, reparse, err := statWindowsLookup(ctx, handle, metadata)
		if err != nil {
			return lookupFile{}, err
		}
		if reparse {
			return lookupFile{symlink: true}, errLookupDeferred
		}
		if len(parts) == 0 {
			return lookupFile{info: info}, nil
		}
		if !info.IsDir() {
			return lookupFile{}, fs.ErrNotExist
		}
		next, err := openWindowsLookup(ctx, windowsLookupOpen{
			parent: handle, name: parts[0], directory: len(parts) > 1,
		}, metadata)
		if err != nil {
			return lookupFile{}, err
		}
		metadata.close(handle)
		handle, parts = next, parts[1:]
	}
}

func openWindowsLookup(
	ctx context.Context,
	request windowsLookupOpen,
	metadata windowsLookupMetadata,
) (uintptr, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	handle, err := metadata.open(request)
	if canceled := ctx.Err(); canceled != nil {
		if err == nil {
			metadata.close(handle)
		}
		return 0, canceled
	}
	return handle, err
}

func statWindowsLookup(
	ctx context.Context,
	handle uintptr,
	metadata windowsLookupMetadata,
) (fs.FileInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	info, reparse, err := metadata.stat(handle)
	if canceled := ctx.Err(); canceled != nil {
		return nil, false, canceled
	}
	return info, reparse, err
}

const (
	maxWindowsLookupPath      = 32767
	maxWindowsLookupComponent = 255
)

// windowsLookupPath splits a local drive path into its drive and components.
// A trailing separator names a directory, so it never yields an executable.
func windowsLookupPath(path string) (string, []string, bool) {
	path = strings.ReplaceAll(path, "/", `\`)
	if len(path) < 3 || len(path) > maxWindowsLookupPath || path[1:3] != `:\` ||
		!asciiLetter(path[0]) {
		return "", nil, false
	}
	parts, ok := windowsLookupComponents(path[3:])
	if !ok || (len(parts) > 0 && strings.HasSuffix(path, `\`)) {
		return "", nil, false
	}
	return path[:2], parts, true
}

func windowsLookupComponents(rest string) ([]string, bool) {
	var parts []string
	for part := range strings.SplitSeq(rest, `\`) {
		if part == "" {
			continue
		}
		if !windowsLookupComponent(part) {
			return nil, false
		}
		parts = append(parts, part)
	}
	return parts, true
}

// windowsLookupComponent applies the platform path rules to one component. A
// colon would name an alternate data stream, so it is refused as well.
func windowsLookupComponent(part string) bool {
	return len(utf16.Encode([]rune(part))) <= maxWindowsLookupComponent &&
		!dotComponent(part) && !strings.Contains(part, ":") && !unsafeWindowsComponent(part)
}

// localWindowsVolume accepts only a volume of a local disk. Mapped drives,
// network redirectors and optical or virtual devices are refused.
func localWindowsVolume(target string) bool {
	const prefix = `\Device\HarddiskVolume`
	number, found := strings.CutPrefix(target, prefix)
	if !found || number == "" {
		return false
	}
	for _, value := range number {
		if value < '0' || value > '9' {
			return false
		}
	}
	return true
}
