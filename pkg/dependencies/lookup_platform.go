package dependencies

import (
	"io/fs"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

const (
	lookupWindows  = "windows"
	defaultPathExt = ".COM;.EXE;.BAT;.CMD"
)

var (
	windowsReservedNames = []string{"CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$"}
	windowsPortNumbers   = []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³"}
)

func (e LookupEnvironment) separator() string {
	if e.windows {
		return `\`
	}
	return string(filepath.Separator)
}

// localAbsolute accepts a plain absolute path on this machine. On Windows that
// is a drive path, with or without the verbatim prefix. Network shares and
// device namespaces are refused because reading their metadata can reach a
// remote host or touch a device.
func (e LookupEnvironment) localAbsolute(path string) bool {
	if strings.ContainsRune(path, 0) {
		return false
	}
	if !e.windows {
		return filepath.IsAbs(path)
	}
	return windowsLocalAbsolute(path)
}

func windowsLocalAbsolute(path string) bool {
	rest := strings.TrimPrefix(path, `\\?\`)
	verbatim := rest != path
	if len(rest) < 3 || !asciiLetter(rest[0]) || rest[1] != ':' {
		return false
	}
	if rest[2] != '\\' && (rest[2] != '/' || verbatim) {
		return false
	}
	return windowsPathSafe(rest[2:], verbatim)
}

// windowsPathSafe refuses alternate data streams and, for ordinary paths,
// device names in any component. Verbatim paths do not normalize "/" or dot
// components, so those would mean something other than their lexical text.
func windowsPathSafe(tail string, verbatim bool) bool {
	if strings.Contains(tail, ":") {
		return false
	}
	components := slices.Collect(strings.FieldsFuncSeq(tail, windowsSeparator))
	if verbatim {
		return !strings.Contains(tail, "/") && !slices.ContainsFunc(components, dotComponent)
	}
	return !slices.ContainsFunc(components, unsafeWindowsComponent)
}

// unsafeWindowsComponent refuses names Win32 would read differently from their
// text: device names, wildcards, control characters, and trailing dots or spaces.
func unsafeWindowsComponent(component string) bool {
	if dotComponent(component) {
		return false
	}
	return reservedComponent(component) || strings.ContainsAny(component, `*?<>|"`) ||
		strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") ||
		slices.ContainsFunc([]rune(component), isControl)
}

func windowsSeparator(r rune) bool { return r == '\\' || r == '/' }

func dotComponent(component string) bool { return component == "." || component == ".." }

// rootedOrQualified reports Windows forms that depend on ambient drive state or
// are not local, such as C:tool, \tool, /tool and \\host\share.
func (e LookupEnvironment) rootedOrQualified(path string) bool {
	if !e.windows {
		return false
	}
	drive := len(path) >= 2 && asciiLetter(path[0]) && path[1] == ':'
	return drive || strings.HasPrefix(path, `\`) || strings.HasPrefix(path, "/")
}

func asciiLetter(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func invocationPath(command string, windows bool) bool {
	if windows {
		return strings.ContainsAny(command, `:\/`)
	}
	return strings.ContainsRune(command, '/')
}

// commandName reports a bare command name. On Windows it also refuses names
// that the filesystem would read differently: wildcard characters, trailing
// dots or spaces, control characters and reserved device names.
func commandName(command string, windows bool) bool {
	if command == "" || command == "." || command == ".." || strings.ContainsRune(command, 0) ||
		invocationPath(command, windows) {
		return false
	}
	return !windows || windowsCommandSafe(command)
}

func windowsCommandSafe(command string) bool {
	if strings.ContainsAny(command, `*?<>|"`) || strings.HasSuffix(command, ".") ||
		strings.HasSuffix(command, " ") || slices.ContainsFunc([]rune(command), isControl) {
		return false
	}
	return !reservedComponent(command)
}

func reservedComponent(name string) bool {
	stem, _, _ := strings.Cut(name, ".")
	return reservedDeviceName(strings.ToUpper(strings.TrimRight(stem, " ")))
}

func reservedDeviceName(stem string) bool {
	if slices.Contains(windowsReservedNames, stem) {
		return true
	}
	for _, prefix := range []string{"COM", "LPT"} {
		if number, ok := strings.CutPrefix(stem, prefix); ok &&
			slices.Contains(windowsPortNumbers, number) {
			return true
		}
	}
	return false
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// pathExtensions returns the effective PATHEXT entries of the snapshot. Entries
// that could name another file or a path are dropped.
func (e LookupEnvironment) pathExtensions() []string {
	extensions, present := e.Value("PATHEXT")
	if !present || extensions == "" {
		extensions = defaultPathExt
	}
	var valid []string
	for extension := range strings.SplitSeq(extensions, ";") {
		if extension != "" && !strings.HasPrefix(extension, ".") {
			extension = "." + extension
		}
		if validExtension(extension) {
			valid = append(valid, extension)
		}
	}
	return valid
}

func validExtension(extension string) bool {
	body := strings.TrimPrefix(extension, ".")
	return body != "" && !strings.ContainsAny(body, `.:\/*?<>|" `+"\x00") &&
		!slices.ContainsFunc([]rune(body), isControl)
}

func (e LookupEnvironment) executableNames(command string) []string {
	if !e.windows {
		return []string{command}
	}
	extensions := e.pathExtensions()
	if e.knownExtension(e.extension(command), extensions) {
		return []string{command}
	}
	names := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		names = append(names, command+extension)
	}
	return names
}

func (e LookupEnvironment) knownExtension(extension string, extensions []string) bool {
	return extension != "" &&
		slices.ContainsFunc(extensions, func(known string) bool {
			return strings.EqualFold(known, extension)
		})
}

func (e LookupEnvironment) separatorRune(r rune) bool {
	return strings.ContainsRune(e.separators(), r)
}

func (e LookupEnvironment) separators() string {
	if e.windows {
		return `\/`
	}
	return "/"
}

// extension returns the suffix after the last dot of the final path element.
func (e LookupEnvironment) extension(path string) string {
	if cut := strings.LastIndexAny(path, e.separators()); cut >= 0 {
		path = path[cut+1:]
	}
	if dot := strings.LastIndexByte(path, '.'); dot >= 0 {
		return path[dot:]
	}
	return ""
}

// executableFile is evidence only. Callers pass os.Stat output, so a symlink is
// judged by its target. A missing, directory or other non-regular entry is
// never executable. On Windows the suffix has to be a PATHEXT entry, and it
// never proves a native executable format.
func (e LookupEnvironment) executableFile(path string, info fs.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() {
		return false
	}
	if e.windows {
		return e.knownExtension(e.extension(path), e.pathExtensions())
	}
	return info.Mode().Perm()&0o111 != 0
}

// scriptPath marks command interpreter scripts. Callers must not pass untrusted
// arguments to a script, because cmd.exe re-parses them.
func (e LookupEnvironment) scriptPath(path string) bool {
	return slices.Contains([]string{".cmd", ".bat", ".ps1"}, strings.ToLower(e.extension(path)))
}

func (e LookupEnvironment) caseInsensitive() bool {
	return e.windows || runtime.GOOS == "darwin"
}

func (e LookupEnvironment) directory(path string) string {
	if cut := strings.LastIndexAny(path, e.separators()); cut >= 0 {
		if cut == 0 || e.windows && path[cut-1] == ':' {
			return path[:cut+1]
		}
		return path[:cut]
	}
	return "."
}

// indirectLauncher reports whether path may be a version-manager shim. Dot
// components and symlinks hide the real location, so a path containing a dot
// component counts as a launcher, and callers resolve symlinks before asking.
// Case-insensitive matching on darwin can only add matches, which fails closed.
func (e LookupEnvironment) indirectLauncher(path string) bool {
	if slices.ContainsFunc(
		slices.Collect(strings.FieldsFuncSeq(path, e.separatorRune)), dotComponent,
	) {
		return true
	}
	directory := e.directory(path)
	for key, child := range map[string]string{
		"MISE_DATA_DIR": "shims", "ASDF_DATA_DIR": "shims", "AQUA_ROOT_DIR": "bin",
	} {
		root, _ := e.Value(key)
		root = strings.TrimRight(root, e.separators())
		if e.localAbsolute(root) && e.sameDirectory(directory, root+e.separator()+child) {
			return true
		}
	}
	// Only reviewed structural locations; no home discovery or directory scanning.
	normalized := e.normalizeDirectory(directory)
	for _, suffix := range []string{
		`/.local/share/mise/shims`, `/.asdf/shims`, `/.local/share/aquaproj-aqua/bin`,
	} {
		if strings.HasSuffix(normalized, e.normalizeDirectory(suffix)) {
			return true
		}
	}
	return false
}

func (e LookupEnvironment) sameDirectory(left, right string) bool {
	return e.normalizeDirectory(left) == e.normalizeDirectory(right)
}

func (e LookupEnvironment) normalizeDirectory(path string) string {
	separator := e.separator()
	if e.windows {
		path = strings.TrimPrefix(strings.ReplaceAll(path, "/", separator), `\\?\`)
	}
	for strings.Contains(path, separator+separator) {
		path = strings.ReplaceAll(path, separator+separator, separator)
	}
	path = strings.TrimRight(path, separator)
	if e.caseInsensitive() {
		path = strings.ToLower(path)
	}
	return path
}
