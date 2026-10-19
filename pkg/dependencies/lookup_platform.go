package dependencies

import (
	"io/fs"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

const lookupWindows = "windows"

func invocationPath(command string) bool {
	if runtime.GOOS == lookupWindows {
		return strings.ContainsAny(command, `:\/`)
	}
	return strings.ContainsRune(command, '/')
}

func (e LookupEnvironment) executableNames(command string) []string {
	if runtime.GOOS != lookupWindows {
		return []string{command}
	}
	if filepath.Ext(command) != "" {
		return []string{command}
	}
	var names []string
	extensions, present := e.Value("PATHEXT")
	if !present {
		extensions = ".COM;.EXE;.BAT;.CMD"
	}
	for extension := range strings.SplitSeq(extensions, ";") {
		if extension == "" || strings.ContainsAny(extension, `:\/`+"\x00") {
			continue
		}
		if !strings.HasPrefix(extension, ".") {
			extension = "." + extension
		}
		names = append(names, command+extension)
	}
	return names
}

func executableFile(path string, info fs.FileInfo) bool {
	if runtime.GOOS == lookupWindows {
		// An extension is invocation evidence only, never proof of native format.
		return filepath.Ext(path) != ""
	}
	return info.Mode().Perm()&0o111 != 0
}

func scriptPath(path string) bool {
	return slices.Contains([]string{".cmd", ".bat", ".ps1"}, strings.ToLower(filepath.Ext(path)))
}

func (e LookupEnvironment) indirectLauncher(path string) bool {
	directory := filepath.Dir(path)
	for key, child := range map[string]string{"MISE_DATA_DIR": "shims", "ASDF_DATA_DIR": "shims", "AQUA_ROOT_DIR": "bin"} {
		if root, _ := e.Value(
			key,
		); filepath.IsAbs(root) &&
			sameDirectory(directory, filepath.Join(root, child)) {
			return true
		}
	}
	// Only reviewed structural locations; no home discovery or directory scanning.
	normalized := filepath.ToSlash(directory)
	if runtime.GOOS == lookupWindows {
		normalized = strings.ToLower(normalized)
	}
	for _, suffix := range []string{"/.local/share/mise/shims", "/.asdf/shims", "/.local/share/aquaproj-aqua/bin"} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

func sameDirectory(left, right string) bool {
	if runtime.GOOS == lookupWindows {
		return strings.EqualFold(left, right)
	}
	return left == right
}
