package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const (
	homebrewBuildID    = "devsy-homebrew"
	homebrewJobID      = "publish-homebrew"
	goreleaserFile     = ".goreleaser.yml"
	releaseWorkflow    = ".github/workflows/release.yml"
	nonHomebrewBinary  = "devsy-linux-amd64"
	formulaGlobPattern = `Dir\["([^"]+)"\]`
)

var (
	buildIDPattern       = regexp.MustCompile(`--id\s+(\S+)`)
	verifiedAssetPattern = regexp.MustCompile(`devsy-bin/([^"\s]+)"?\s+update`)
)

type goreleaserBuild struct {
	ID     string   `yaml:"id"`
	Goos   []string `yaml:"goos"`
	Goarch []string `yaml:"goarch"`
	Binary string   `yaml:"binary"`
}

type releaseStep struct {
	Run  string         `yaml:"run"`
	Uses string         `yaml:"uses"`
	With map[string]any `yaml:"with"`
}

func readRepoYAML(t *testing.T, file string, target any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(file)))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(raw, target))
}

func homebrewBuild(t *testing.T) goreleaserBuild {
	t.Helper()
	var config struct {
		Builds []goreleaserBuild `yaml:"builds"`
	}
	readRepoYAML(t, goreleaserFile, &config)
	for _, build := range config.Builds {
		if build.ID == homebrewBuildID {
			return build
		}
	}
	require.FailNow(t, "no build with id "+homebrewBuildID)
	return goreleaserBuild{}
}

func homebrewSteps(t *testing.T) []releaseStep {
	t.Helper()
	var workflow struct {
		Jobs map[string]struct {
			Steps []releaseStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	readRepoYAML(t, releaseWorkflow, &workflow)
	job, ok := workflow.Jobs[homebrewJobID]
	require.True(t, ok, "release workflow has no %s job", homebrewJobID)
	return job.Steps
}

func (b goreleaserBuild) binaryNames(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, goos := range b.Goos {
		for _, goarch := range b.Goarch {
			name := strings.NewReplacer("{{ .Os }}", goos, "{{ .Arch }}", goarch).Replace(b.Binary)
			require.NotContains(t, name, "{{", "unexpanded template in %q", b.Binary)
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func formulaBinaries() []string {
	names := make([]string, 0, len(platforms))
	for _, p := range platforms {
		names = append(names, p.Binary)
	}
	slices.Sort(names)
	return names
}

func formulaAssetGlob(t *testing.T) string {
	t.Helper()
	match := regexp.MustCompile(formulaGlobPattern).FindStringSubmatch(formulaTmpl)
	require.Len(t, match, 2, "formula template does not install a globbed asset")
	return match[1]
}

func findStep(t *testing.T, steps []releaseStep, want func(releaseStep) bool) releaseStep {
	t.Helper()
	for _, step := range steps {
		if want(step) {
			return step
		}
	}
	require.FailNow(t, "release workflow step not found")
	return releaseStep{}
}

func TestGoReleaserBuildsTheFormulaAssets(t *testing.T) {
	require.Equal(t, formulaBinaries(), homebrewBuild(t).binaryNames(t))
}

func TestFormulaGlobSelectsOnlyHomebrewAssets(t *testing.T) {
	glob := formulaAssetGlob(t)

	for _, name := range formulaBinaries() {
		matched, err := path.Match(glob, name)
		require.NoError(t, err)
		require.True(t, matched, "%s does not match %s", name, glob)
	}
	matched, err := path.Match(glob, nonHomebrewBinary)
	require.NoError(t, err)
	require.False(t, matched, "%s matches %s", nonHomebrewBinary, glob)
}

func TestReleaseWorkflowBuildsStagesAndUploadsTheFormulaAssets(t *testing.T) {
	steps := homebrewSteps(t)
	glob := formulaAssetGlob(t)

	build := findStep(t, steps, func(s releaseStep) bool {
		return strings.Contains(fmt.Sprint(s.With["args"]), "build")
	})
	require.Equal(t, homebrewBuildID, capture(t, buildIDPattern, fmt.Sprint(build.With["args"])))

	stage := findStep(
		t,
		steps,
		func(s releaseStep) bool { return strings.Contains(s.Run, "find dist") },
	)
	require.Contains(t, stage.Run, "-name '"+glob+"'")

	upload := findStep(t, steps, func(s releaseStep) bool {
		return strings.Contains(s.Uses, "action-gh-release")
	})
	require.True(t, strings.HasSuffix(fmt.Sprint(upload.With["files"]), "/"+glob))
}

func TestReleaseWorkflowChecksAnAssetTheFormulaServes(t *testing.T) {
	verify := findStep(t, homebrewSteps(t), func(s releaseStep) bool {
		return strings.Contains(s.Run, "brew upgrade devsy")
	})

	asset := capture(t, verifiedAssetPattern, verify.Run)
	require.Contains(
		t,
		formulaBinaries(),
		asset,
		"verify step runs an asset the formula does not serve",
	)
}

func capture(t *testing.T, pattern *regexp.Regexp, text string) string {
	t.Helper()
	match := pattern.FindStringSubmatch(text)
	require.Len(t, match, 2, "%s not found in:\n%s", pattern, text)
	return match[1]
}
