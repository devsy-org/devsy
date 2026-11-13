package dependencies

import (
	"context"
	"io/fs"
	"strings"
)

// Each open is either a validated native disk root or a single child of a pinned
// handle. Implementations must open the reparse point itself and never follow it.
type windowsLookupMetadata interface {
	driveTarget(drive string) (string, error)
	open(parent uintptr, name string) (uintptr, error)
	stat(handle uintptr) (info fs.FileInfo, reparse bool, err error)
	close(handle uintptr)
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
	handle, err := openWindowsLookup(ctx, 0, target+`\`, metadata)
	if err != nil {
		return lookupFile{}, err
	}
	observed, err := walkWindowsLookup(ctx, handle, parts, metadata)
	if err == nil {
		observed.resolved = path
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
		next, err := openWindowsLookup(ctx, handle, parts[0], metadata)
		if err != nil {
			return lookupFile{}, err
		}
		metadata.close(handle)
		handle, parts = next, parts[1:]
	}
}

func openWindowsLookup(
	ctx context.Context,
	parent uintptr,
	name string,
	metadata windowsLookupMetadata,
) (uintptr, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	handle, err := metadata.open(parent, name)
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

func windowsLookupPath(path string) (string, []string, bool) {
	path = strings.ReplaceAll(path, "/", `\`)
	if len(path) < 3 || path[1:3] != `:\` || !asciiDriveLetter(path[0]) {
		return "", nil, false
	}
	var parts []string
	for part := range strings.SplitSeq(path[3:], `\`) {
		if part == "" {
			continue
		}
		if !windowsLookupComponent(part) {
			return "", nil, false
		}
		parts = append(parts, part)
	}
	return path[:2], parts, true
}

func asciiDriveLetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func windowsLookupComponent(part string) bool {
	if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") ||
		strings.ContainsAny(part, ":<>\"|?*") {
		return false
	}
	for _, value := range part {
		if value < 32 {
			return false
		}
	}
	base, _, _ := strings.Cut(strings.ToUpper(part), ".")
	base = strings.TrimRight(base, " ")
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return false
	}
	return !windowsNumberedDevice(base)
}

func windowsNumberedDevice(base string) bool {
	runes := []rune(base)
	return len(runes) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) &&
		strings.ContainsRune("123456789\u00b9\u00b2\u00b3", runes[3])
}

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
