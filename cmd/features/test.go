package features

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/spf13/cobra"
)

const defaultBaseImage = "mcr.microsoft.com/devcontainers/base:ubuntu"

type TestCmd struct {
	*flags.GlobalFlags

	ProjectFolder          string
	Features               string
	BaseImage              string
	RemoteUser             string
	SkipScenarios          bool
	Quiet                  bool
	PreserveTestContainers bool
}

type testResult struct {
	FeatureID string `json:"featureId"`
	Scenario  string `json:"scenario,omitempty"`
	Passed    bool   `json:"passed"`
	Error     string `json:"error,omitempty"`
}

func NewTestCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &TestCmd{GlobalFlags: globalFlags}
	testCmd := &cobra.Command{
		Use:   "test",
		Short: "Test dev container features in isolation",
		Long: `Run lifecycle hook tests for dev container features.

Scans the project's src/ directory for features, builds test containers
from a base image, installs each feature, and runs the corresponding
test scripts from the test/ directory.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return cmd.Run()
		},
	}

	testCmd.Flags().StringVar(
		&cmd.ProjectFolder, "project-folder", "",
		"Path to feature project containing src/ and test/ directories",
	)
	testCmd.Flags().StringVar(
		&cmd.Features, "features", "",
		"Comma-separated list of feature IDs to test (default: all)",
	)
	testCmd.Flags().StringVar(
		&cmd.BaseImage, "base-image", defaultBaseImage,
		"Base Docker image for test containers",
	)
	testCmd.Flags().StringVar(
		&cmd.RemoteUser, "remote-user", "root",
		"User to run tests as",
	)
	testCmd.Flags().BoolVar(
		&cmd.SkipScenarios, "skip-scenarios", false,
		"Only run the global test script, skip per-feature scenario tests",
	)
	testCmd.Flags().BoolVar(
		&cmd.Quiet, "quiet", false,
		"Suppress verbose build output",
	)
	testCmd.Flags().BoolVar(
		&cmd.PreserveTestContainers, "preserve-test-containers", false,
		"Don't remove test containers after run (for debugging)",
	)
	_ = testCmd.MarkFlagRequired("project-folder")

	return testCmd
}

func (cmd *TestCmd) Run() error {
	projectFolder, err := filepath.Abs(cmd.ProjectFolder)
	if err != nil {
		return fmt.Errorf("resolve project folder: %w", err)
	}

	srcDir := filepath.Join(projectFolder, "src")
	testDir := filepath.Join(projectFolder, "test")

	features, err := cmd.discoverFeatures(srcDir)
	if err != nil {
		return err
	}

	features = cmd.filterFeatures(features)
	if len(features) == 0 {
		return fmt.Errorf("no features matched the filter %q", cmd.Features)
	}

	var results []testResult
	for _, feat := range features {
		featureResults := cmd.testFeature(feat, projectFolder, testDir)
		results = append(results, featureResults...)
	}

	cmd.printResults(results)

	for _, r := range results {
		if !r.Passed {
			return fmt.Errorf("one or more feature tests failed")
		}
	}
	return nil
}

type featureEntry struct {
	id     string
	config *config.FeatureConfig
}

func (cmd *TestCmd) discoverFeatures(srcDir string) ([]featureEntry, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil, fmt.Errorf("read src/ directory: %w", err)
	}

	var features []featureEntry
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		featureDir := filepath.Join(srcDir, entry.Name())
		featureCfg, parseErr := config.ParseDevContainerFeature(featureDir)
		if parseErr != nil {
			continue
		}

		features = append(features, featureEntry{
			id:     entry.Name(),
			config: featureCfg,
		})
	}

	if len(features) == 0 {
		return nil, fmt.Errorf("no features found in %s", srcDir)
	}
	return features, nil
}

func (cmd *TestCmd) filterFeatures(features []featureEntry) []featureEntry {
	if cmd.Features == "" {
		return features
	}

	filter := make(map[string]bool)
	for f := range strings.SplitSeq(cmd.Features, ",") {
		filter[strings.TrimSpace(f)] = true
	}

	var filtered []featureEntry
	for _, feat := range features {
		if filter[feat.id] {
			filtered = append(filtered, feat)
		}
	}
	return filtered
}

type testCase struct {
	script   string
	scenario string
	options  map[string]string
}

func (cmd *TestCmd) testFeature(
	feat featureEntry, projectFolder, testDir string,
) []testResult {
	var results []testResult

	featureTestDir := filepath.Join(testDir, feat.id)
	globalTestScript := filepath.Join(featureTestDir, "test.sh")

	if _, err := os.Stat(globalTestScript); err == nil {
		tc := testCase{script: globalTestScript}
		results = append(results, cmd.runTest(feat, projectFolder, tc))
	}

	if cmd.SkipScenarios {
		return results
	}

	scenariosDir := filepath.Join(featureTestDir, "scenarios")
	scenarios, err := os.ReadDir(scenariosDir)
	if err != nil {
		return results
	}

	for _, scenario := range scenarios {
		if !scenario.IsDir() {
			continue
		}

		scenarioDir := filepath.Join(scenariosDir, scenario.Name())
		scenarioTestScript := filepath.Join(scenarioDir, "test.sh")

		if _, err := os.Stat(scenarioTestScript); err != nil {
			continue
		}

		tc := testCase{
			script:   scenarioTestScript,
			scenario: scenario.Name(),
			options:  cmd.loadScenarioOptions(scenarioDir),
		}
		results = append(results, cmd.runTest(feat, projectFolder, tc))
	}

	return results
}

func (cmd *TestCmd) loadScenarioOptions(scenarioDir string) map[string]string {
	scenarioJSON := filepath.Join(scenarioDir, "scenario.json")
	data, err := os.ReadFile(scenarioJSON) // #nosec G304 -- path from project structure
	if err != nil {
		return nil
	}

	var scenario struct {
		Options map[string]string `json:"options"`
	}
	if err := json.Unmarshal(data, &scenario); err != nil {
		return nil
	}
	return scenario.Options
}

func (cmd *TestCmd) runTest(
	feat featureEntry, projectFolder string, tc testCase,
) testResult {
	result := testResult{
		FeatureID: feat.id,
		Scenario:  tc.scenario,
	}

	dockerfile := cmd.generateDockerfile(feat, tc.options)

	containerName := fmt.Sprintf("devsy-test-%s", feat.id)
	if tc.scenario != "" {
		containerName = fmt.Sprintf("devsy-test-%s-%s", feat.id, tc.scenario)
	}

	imageName := containerName + ":latest"

	buildErr := cmd.dockerBuild(dockerfile, projectFolder, imageName)
	if buildErr != nil {
		result.Passed = false
		result.Error = buildErr.Error()
		return result
	}

	runErr := cmd.dockerRun(imageName, containerName, tc.script)
	if runErr != nil {
		result.Passed = false
		result.Error = runErr.Error()
	} else {
		result.Passed = true
	}

	if !cmd.PreserveTestContainers {
		cmd.dockerRemove(containerName)
	}

	return result
}

func (cmd *TestCmd) generateDockerfile(
	feat featureEntry, options map[string]string,
) string {
	var b strings.Builder

	fmt.Fprintf(&b, "FROM %s\n", cmd.BaseImage)

	featureSrcDir := filepath.Join("src", feat.id)
	fmt.Fprintf(&b, "COPY %s /tmp/build-features/%s\n", featureSrcDir, feat.id)

	for k, v := range options {
		envKey := strings.ToUpper(feat.id) + "_" + strings.ToUpper(k)
		fmt.Fprintf(&b, "ENV %s=%s\n", envKey, v)
	}

	fmt.Fprintf(
		&b,
		"RUN chmod +x /tmp/build-features/%s/install.sh && /tmp/build-features/%s/install.sh\n",
		feat.id, feat.id,
	)

	if cmd.RemoteUser != "root" {
		fmt.Fprintf(&b, "USER %s\n", cmd.RemoteUser)
	}

	return b.String()
}

func (cmd *TestCmd) dockerBuild(dockerfile, contextDir, imageName string) error {
	args := []string{"build", "-t", imageName, "-f", "-", contextDir}
	dockerCmd := exec.Command("docker", args...) // #nosec G204 -- args built from trusted inputs
	dockerCmd.Stdin = strings.NewReader(dockerfile)

	if !cmd.Quiet {
		dockerCmd.Stdout = os.Stdout
		dockerCmd.Stderr = os.Stderr
	}

	return dockerCmd.Run()
}

func (cmd *TestCmd) dockerRun(imageName, containerName, testScript string) error {
	testContent, err := os.ReadFile(testScript) // #nosec G304 -- path from project test directory
	if err != nil {
		return fmt.Errorf("read test script: %w", err)
	}

	args := []string{
		"run", "--name", containerName,
		"--rm",
		imageName,
		"bash", "-c", string(testContent),
	}
	dockerCmd := exec.Command("docker", args...) // #nosec G204 -- args built from trusted inputs

	if !cmd.Quiet {
		dockerCmd.Stdout = os.Stdout
		dockerCmd.Stderr = os.Stderr
	}

	return dockerCmd.Run()
}

func (cmd *TestCmd) dockerRemove(containerName string) {
	rmCmd := exec.Command("docker", "rm", "-f", containerName) // #nosec G204
	_ = rmCmd.Run()

	rmiCmd := exec.Command("docker", "rmi", "-f", containerName+":latest") // #nosec G204
	_ = rmiCmd.Run()
}

func (cmd *TestCmd) printResults(results []testResult) {
	w := os.Stdout
	_, _ = fmt.Fprintln(w, "\n=== Feature Test Results ===")
	passed := 0
	failed := 0

	for _, r := range results {
		label := r.FeatureID
		if r.Scenario != "" {
			label = fmt.Sprintf("%s/%s", r.FeatureID, r.Scenario)
		}

		if r.Passed {
			_, _ = fmt.Fprintf(w, "  PASS: %s\n", label)
			passed++
		} else {
			_, _ = fmt.Fprintf(w, "  FAIL: %s — %s\n", label, r.Error)
			failed++
		}
	}

	_, _ = fmt.Fprintf(w, "\nTotal: %d passed, %d failed\n", passed, failed)
}

// GenerateDockerfileForTest exposes Dockerfile generation for unit testing.
func GenerateDockerfileForTest(
	featureID, baseImage, remoteUser string,
	options map[string]string,
) string {
	cmd := &TestCmd{
		BaseImage:  baseImage,
		RemoteUser: remoteUser,
	}
	feat := featureEntry{
		id:     featureID,
		config: &config.FeatureConfig{ID: featureID},
	}
	return cmd.generateDockerfile(feat, options)
}
