package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const desktopEntryGroup = "[Desktop Entry]"

func verifyLinuxDesktopCmd() *cobra.Command {
	var appID, desktopFile, iconRoot string
	var requiredFiles []string
	cmd := &cobra.Command{
		Use:   "verify-linux-desktop",
		Short: "assert a Linux package's desktop entry, window class and icon agree with the app ID",
		RunE: func(_ *cobra.Command, _ []string) error {
			return verifyLinuxDesktop(appID, desktopFile, iconRoot, requiredFiles)
		},
	}
	cmd.Flags().StringVar(&appID, "app-id", "", "application ID, e.g. sh.devsy.app")
	cmd.Flags().
		StringVar(&desktopFile, "desktop-file", "", "desktop entry extracted from the package")
	cmd.Flags().
		StringVar(&iconRoot, "icon-root", "", "directory searched for the PNG named by the entry's Icon key")
	cmd.Flags().
		StringArrayVar(&requiredFiles, "require-file", nil, "additional file that must exist and be non-empty")
	for _, f := range []string{"app-id", "desktop-file", "icon-root"} {
		_ = cmd.MarkFlagRequired(f)
	}
	return cmd
}

func verifyLinuxDesktop(appID, desktopFile, iconRoot string, requiredFiles []string) error {
	if got, want := filepath.Base(desktopFile), appID+".desktop"; got != want {
		return fmt.Errorf("::error::desktop entry is named %s, expected %s", got, want)
	}
	entry, err := readDesktopEntry(desktopFile)
	if err != nil {
		return fmt.Errorf("::error::%s: %w", desktopFile, err)
	}
	if got := entry["StartupWMClass"]; got != appID {
		return fmt.Errorf("::error::%s: StartupWMClass is %q, expected %q", desktopFile, got, appID)
	}
	icon := entry["Icon"]
	if icon == "" {
		return fmt.Errorf("::error::%s: no Icon key", desktopFile)
	}
	iconPath, err := findNonEmptyFile(iconRoot, icon+".png")
	if err != nil {
		return fmt.Errorf("::error::icon %q named by %s: %w", icon, desktopFile, err)
	}
	fmt.Printf("desktop=%s class=%s icon=%s\n", desktopFile, appID, iconPath)
	for _, rel := range requiredFiles {
		if err := requireNonEmptyFile(rel); err != nil {
			return fmt.Errorf("::error::%w", err)
		}
	}
	return nil
}

func readDesktopEntry(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	entry := map[string]string{}
	inGroup := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			inGroup = line == desktopEntryGroup
			continue
		}
		if !inGroup {
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			entry[key] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entry, nil
}

var errFileNotFound = errors.New("not found")

func findNonEmptyFile(root, name string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || d.Name() != name {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.Size() > 0 {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("no non-empty %s under %s: %w", name, root, errFileNotFound)
	}
	return found, nil
}

func requireNonEmptyFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() || info.Size() == 0 {
		return fmt.Errorf("%s is not a non-empty file", path)
	}
	return nil
}
