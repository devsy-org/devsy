package selfupdate

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/version"
)

type Options struct {
	Version           string
	DryRun            bool
	IncludePrerelease bool
}

// Upgrade downloads the latest release from github and replaces devsy if a new version is found.
// If DryRun is true, it only shows what would be downloaded without actually upgrading.
func Upgrade(ctx context.Context, opts Options) error {
	release, updater, err := detectRelease(ctx, opts)
	if err != nil {
		return err
	}

	if release.Version() == strings.TrimLeft(version.GetVersion(), "v") {
		if _, err := fmt.Fprintf(
			os.Stdout,
			"devsy version %s is already up-to-date\n",
			release.Version(),
		); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
		return nil
	}

	if opts.DryRun {
		dryRunOutput := fmt.Sprintf(
			"asset_name=%s\nversion=%s\nos=%s\narch=%s\nurl=%s\nsize=%d\n",
			release.AssetName,
			release.Version(),
			release.OS,
			release.Arch,
			release.AssetURL,
			release.AssetByteSize,
		)
		if _, err := fmt.Fprint(os.Stdout, dryRunOutput); err != nil {
			return fmt.Errorf("write dry-run output: %w", err)
		}
		return nil
	}

	cmdPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}

	log.Infof("downloading version %s", release.Version())
	if err := updater.UpdateTo(ctx, release, cmdPath); err != nil {
		return fmt.Errorf("update to version %s: %w", release.Version(), err)
	}

	log.Infof("updated devsy to version %s", release.Version())
	return nil
}

func detectRelease(
	ctx context.Context,
	opts Options,
) (*selfupdate.Release, *selfupdate.Updater, error) {
	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Prerelease: opts.IncludePrerelease,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("initialize updater: %w", err)
	}

	repo := selfupdate.ParseSlug(config.RepoSlug)

	if opts.Version != "" {
		release, err := detectSpecificVersion(ctx, updater, repo, opts.Version)
		return release, updater, err
	}

	release, err := detectLatestVersion(ctx, updater, repo)
	return release, updater, err
}

func detectSpecificVersion(
	ctx context.Context,
	updater *selfupdate.Updater,
	repo selfupdate.RepositorySlug,
	version string,
) (*selfupdate.Release, error) {
	release, found, err := updater.DetectVersion(ctx, repo, version)
	if err != nil {
		return nil, fmt.Errorf("detect version %s: %w", version, err)
	}
	if !found {
		return nil, fmt.Errorf("version %s not found", version)
	}
	return release, nil
}

func detectLatestVersion(
	ctx context.Context,
	updater *selfupdate.Updater,
	repo selfupdate.RepositorySlug,
) (*selfupdate.Release, error) {
	release, found, err := updater.DetectLatest(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("detect latest version: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("no release found")
	}
	return release, nil
}
