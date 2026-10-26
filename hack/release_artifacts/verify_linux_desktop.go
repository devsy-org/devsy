package main

import (
	"bufio"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

const desktopEntryGroup = "[Desktop Entry]"

var themeIconName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func verifyLinuxDesktopCmd() *cobra.Command {
	var appID, desktopFile, iconRoot string
	var requiredFiles []string
	cmd := &cobra.Command{
		Use:   "verify-linux-desktop",
		Short: "verify desktop identity and decoded icons in an extracted Linux package",
		RunE: func(_ *cobra.Command, _ []string) error {
			return verifyLinuxDesktop(appID, desktopFile, iconRoot, requiredFiles)
		},
	}
	cmd.Flags().StringVar(&appID, "app-id", "", "expected application ID")
	cmd.Flags().StringVar(&desktopFile, "desktop-file", "", "packaged desktop entry")
	cmd.Flags().StringVar(&iconRoot, "icon-root", "", "package's share/icons/hicolor directory")
	cmd.Flags().
		StringArrayVar(&requiredFiles, "require-file", nil, "additional PNG to decode (runtime or AppDir icon)")
	for _, f := range []string{"app-id", "desktop-file", "icon-root"} {
		_ = cmd.MarkFlagRequired(f)
	}
	return cmd
}

func verifyLinuxDesktop(appID, desktopFile, iconRoot string, requiredFiles []string) error {
	icon, err := desktopIdentity(appID, desktopFile)
	if err != nil {
		return err
	}
	if err := verifyThemeIcons(iconRoot, icon); err != nil {
		return err
	}
	for _, path := range requiredFiles {
		if err := verifyPNG(path, 0); err != nil {
			return err
		}
	}
	fmt.Printf("desktop=%s class=%s icon=%s: decoded package PNGs\n", desktopFile, appID, icon)
	return nil
}

func desktopIdentity(appID, desktopFile string) (string, error) {
	if got, want := filepath.Base(desktopFile), appID+".desktop"; got != want {
		return "", fmt.Errorf("desktop entry is named %s, expected %s", got, want)
	}
	entry, err := readDesktopEntry(desktopFile)
	if err != nil {
		return "", fmt.Errorf("%s: %w", desktopFile, err)
	}
	if got := entry["StartupWMClass"]; got != appID {
		return "", fmt.Errorf("%s: StartupWMClass is %q, expected %q", desktopFile, got, appID)
	}
	// Builder keeps the executable's icon name even with syncDesktopName.
	icon := entry["Icon"]
	if !themeIconName.MatchString(icon) {
		return "", fmt.Errorf("%s: invalid theme Icon %q", desktopFile, icon)
	}
	return icon, nil
}

func verifyThemeIcons(iconRoot, icon string) error {
	paths, err := filepath.Glob(filepath.Join(iconRoot, "*", "apps", icon+".png"))
	if err != nil {
		return err
	}
	found128 := false
	for _, path := range paths {
		if err := verifyThemePNG(path); err != nil {
			return err
		}
		size := filepath.Base(filepath.Dir(filepath.Dir(path)))
		found128 = found128 || size == "128x128"
	}
	if !found128 {
		return fmt.Errorf(
			"missing 128x128 theme icon at %s",
			filepath.Join(iconRoot, "128x128", "apps", icon+".png"),
		)
	}
	return nil
}

func verifyThemePNG(path string) error {
	size := filepath.Base(filepath.Dir(filepath.Dir(path)))
	width, height, ok := strings.Cut(size, "x")
	n, parseErr := strconv.Atoi(width)
	if !ok || width != height || parseErr != nil || n < 1 || n > 512 {
		return fmt.Errorf("invalid hicolor size directory %q", size)
	}
	return verifyPNG(path, n)
}

func verifyPNG(path string, expectedSize int) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	config, err := png.DecodeConfig(file)
	if err != nil {
		return fmt.Errorf("%s: invalid PNG: %w", path, err)
	}
	if err := verifyPNGDimensions(path, config.Width, config.Height, expectedSize); err != nil {
		return err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	if _, err := png.Decode(file); err != nil {
		return fmt.Errorf("%s: malformed PNG data: %w", path, err)
	}
	return nil
}

func verifyPNGDimensions(path string, width, height, expectedSize int) error {
	if width != height || width < 1 || width > 1024 ||
		(expectedSize != 0 && width != expectedSize) {
		return fmt.Errorf(
			"%s: PNG dimensions %dx%d do not match expected square size %d (max 1024)",
			path,
			width,
			height,
			expectedSize,
		)
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
		if !inGroup || strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			entry[key] = value
		}
	}
	return entry, scanner.Err()
}
