package util

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const homeEnvPlan9 = "home"

// UserHomeDir returns the home directory for the executing user.
//
// This extends the logic of os.UserHomeDir() with the now archived package
// github.com/mitchellh/go-homedir for compatibility.
func UserHomeDir() (string, error) {
	// Always try the HOME environment variable first
	if home := homeFromEnv(); home != "" {
		return home, nil
	}

	// Rely on os.UserHomeDir() here, as it's the standard method moving forward
	if home, _ := os.UserHomeDir(); home != "" {
		return home, nil
	}

	// Handle cases that existed in go-homedir but not in the current
	// os.UserHomeDir() implementation.
	if home, done, err := homeFromPlatform(); done {
		return home, err
	}

	// If all else fails, try the shell
	return homeFromShell()
}

func homeFromEnv() string {
	homeEnv := "HOME"
	if runtime.GOOS == "plan9" {
		homeEnv = homeEnvPlan9
	}
	return os.Getenv(homeEnv)
}

// homeFromPlatform resolves the home directory using platform-specific
// mechanisms. done reports whether the resolution is authoritative; when
// false the caller should fall back to the shell.
func homeFromPlatform() (string, bool, error) {
	switch runtime.GOOS {
	case "windows":
		home, err := homeFromWindows()
		return home, true, err
	case "darwin":
		if home := homeFromDarwin(); home != "" {
			return home, true, nil
		}
	default:
		if home, done, err := homeFromGetent(); done {
			return home, true, err
		}
	}
	return "", false, nil
}

func homeFromWindows() (string, error) {
	drive := os.Getenv("HOMEDRIVE")
	path := os.Getenv("HOMEPATH")
	if drive == "" || path == "" {
		return "", errors.New("HOMEDRIVE, HOMEPATH, or USERPROFILE are blank")
	}
	return drive + path, nil
}

func homeFromDarwin() string {
	var stdout bytes.Buffer
	cmd := exec.Command(
		"sh",
		"-c",
		`dscl -q . -read /Users/"$(whoami)" NFSHomeDirectory | sed 's/^[^ ]*: //'`,
	)
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(stdout.String())
}

func homeFromGetent() (home string, done bool, err error) {
	var stdout bytes.Buffer
	// #nosec G204 -- shell/utility path resolved internally, arguments are not user-tainted
	cmd := exec.Command("getent", "passwd", strconv.Itoa(os.Getuid()))
	cmd.Stdout = &stdout
	if runErr := cmd.Run(); runErr != nil {
		// If the error is ErrNotFound, we return it. Otherwise, we ignore it.
		if errors.Is(runErr, exec.ErrNotFound) {
			return "", true, runErr
		}
		return "", false, nil
	}
	passwd := strings.TrimSpace(stdout.String())
	if passwd == "" {
		return "", false, nil
	}
	// username:password:uid:gid:gecos:home:shell
	passwdParts := strings.SplitN(passwd, ":", 7)
	if len(passwdParts) > 5 && passwdParts[5] != "" {
		return passwdParts[5], true, nil
	}
	return "", false, nil
}

func homeFromShell() (string, error) {
	if runtime.GOOS == "windows" {
		return "", errors.New("can't determine the home directory")
	}

	var stdout bytes.Buffer
	cmd := exec.Command("sh", "-c", "cd && pwd")
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}

	result := strings.TrimSpace(stdout.String())
	if result == "" {
		return "", errors.New("blank output when reading home directory")
	}

	return result, nil
}

// ExpandTilde expands environment variables and a leading ~ or ~/ to the
// user's home directory. If the home directory cannot be determined, the
// original path is returned unchanged.
func ExpandTilde(path string) string {
	path = os.ExpandEnv(path)
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}
