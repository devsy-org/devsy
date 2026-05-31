package feature

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/devsy-org/devsy/cmd/flags"
	devconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/spf13/cobra"
)

const (
	tagLatest          = "latest"
	noFeaturesMessage  = "No features found."
	allUpToDateMessage = "All features are up to date."
)

// OutdatedCmd checks for newer versions of installed devcontainer features.
type OutdatedCmd struct {
	*flags.GlobalFlags

	WorkspaceFolder string
	Config          string
}

// NewOutdatedCmd creates a new outdated command.
func NewOutdatedCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &OutdatedCmd{GlobalFlags: f}
	outdatedCmd := &cobra.Command{
		Use:   "outdated",
		Short: "Checks for newer versions of installed devcontainer features",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}

	outdatedCmd.Flags().
		StringVar(&cmd.WorkspaceFolder, "workspace-folder", "", "Path to the workspace folder")
	outdatedCmd.Flags().
		StringVar(&cmd.Config, "config", "", "Path to a specific devcontainer.json")

	return outdatedCmd
}

// Run runs the command logic.
func (cmd *OutdatedCmd) Run(_ context.Context) error {
	parsedConfig, err := cmd.loadConfig()
	if err != nil {
		return err
	}

	if len(parsedConfig.Features) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, noFeaturesMessage)
		return nil
	}

	outdated := checkOutdatedFeatures(parsedConfig.Features)
	if len(outdated) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, allUpToDateMessage)
		return nil
	}

	printOutdatedTable(outdated)
	return nil
}

func (cmd *OutdatedCmd) loadConfig() (*devconfig.DevContainerConfig, error) {
	workspaceFolder := cmd.WorkspaceFolder
	if workspaceFolder == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
		workspaceFolder = cwd
	}

	workspaceFolder, err := filepath.Abs(workspaceFolder)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace folder: %w", err)
	}

	var parsedConfig *devconfig.DevContainerConfig
	if cmd.Config != "" {
		parsedConfig, err = devconfig.ParseDevContainerJSONFile(cmd.Config)
	} else {
		parsedConfig, err = devconfig.ParseDevContainerJSON(workspaceFolder, "")
	}
	if err != nil {
		return nil, fmt.Errorf("parse devcontainer config: %w", err)
	}
	if parsedConfig == nil {
		return nil, fmt.Errorf("no devcontainer configuration found in %s", workspaceFolder)
	}

	return parsedConfig, nil
}

func printOutdatedTable(outdated []outdatedEntry) {
	sort.Slice(outdated, func(i, j int) bool {
		return outdated[i].repo < outdated[j].repo
	})

	rows := make([][]string, 0, len(outdated))
	for _, entry := range outdated {
		rows = append(rows, []string{entry.repo, entry.current, entry.latest})
	}

	table.Print([]string{"Feature", "Current", "Latest"}, rows)
}

type outdatedEntry struct {
	repo    string
	current string
	latest  string
}

func checkOutdatedFeatures(features map[string]any) []outdatedEntry {
	var results []outdatedEntry
	for featureID := range features {
		entry, ok := checkFeatureVersion(featureID)
		if ok {
			results = append(results, entry)
		}
	}
	return results
}

func isNonOCIFeature(featureID string) bool {
	if strings.HasPrefix(featureID, "./") || strings.HasPrefix(featureID, "../") {
		return true
	}
	return strings.HasPrefix(featureID, "https://") || strings.HasPrefix(featureID, "http://")
}

func parseFeatureTag(featureID string) (name.Tag, bool) {
	ref, err := name.ParseReference(featureID)
	if err != nil {
		log.Debugf("failed to parse feature reference %s: %v", featureID, err)
		return name.Tag{}, false
	}

	tag, ok := ref.(name.Tag)
	if !ok {
		return name.Tag{}, false
	}

	if tag.TagStr() == tagLatest {
		return name.Tag{}, false
	}

	return tag, true
}

func checkFeatureVersion(featureID string) (outdatedEntry, bool) {
	if isNonOCIFeature(featureID) {
		return outdatedEntry{}, false
	}

	tag, ok := parseFeatureTag(featureID)
	if !ok {
		return outdatedEntry{}, false
	}

	currentTag := tag.TagStr()
	repo := tag.Repository

	tags, err := remote.List(repo, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	if err != nil {
		log.Warnf("failed to list tags for %s: %v", repo.Name(), err)
		return outdatedEntry{}, false
	}

	latestTag := findLatestVersion(currentTag, tags)
	if latestTag == "" || latestTag == currentTag {
		return outdatedEntry{}, false
	}

	return outdatedEntry{
		repo:    repo.Name(),
		current: currentTag,
		latest:  latestTag,
	}, true
}

// findLatestVersion finds the highest semver tag from the list that is newer
// than the current tag. Returns empty string if current is already the latest.
func findLatestVersion(current string, tags []string) string {
	currentVer, currentErr := parseSemver(current)

	var best semver.Version
	var bestTag string
	hasBest := false

	for _, t := range tags {
		if t == tagLatest {
			continue
		}

		ver, err := parseSemver(t)
		if err != nil {
			continue
		}

		// If current tag is not valid semver, we cannot compare
		if currentErr != nil {
			continue
		}

		if ver.GT(currentVer) && (!hasBest || ver.GT(best)) {
			best = ver
			bestTag = t
			hasBest = true
		}
	}

	return bestTag
}

// parseSemver parses a version string that may be a bare major ("1"),
// major.minor ("1.21"), or full semver ("1.21.0").
func parseSemver(tag string) (semver.Version, error) {
	parts := strings.Split(tag, ".")
	switch len(parts) {
	case 1:
		// Bare major version like "1" or "20"
		tag += ".0.0"
	case 2:
		// Major.minor like "1.21"
		tag += ".0"
	}
	return semver.Parse(tag)
}
