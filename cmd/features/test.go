package features

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	devsycopy "github.com/devsy-org/devsy/pkg/copy"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

// TestCmd holds the flags for the features test command.
type TestCmd struct {
	*flags.GlobalFlags

	BaseImage     string
	RemoteUser    string
	Output        string
	SkipScenarios bool
	ProjectFolder string
}

type testResult struct {
	FeatureID string           `json:"featureId"`
	Passed    bool             `json:"passed"`
	Scenarios []scenarioResult `json:"scenarios"`
}

type scenarioResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Output string `json:"output,omitempty"`
}

// NewTestCmd creates the features test cobra command.
func NewTestCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &TestCmd{GlobalFlags: globalFlags}
	testCmd := &cobra.Command{
		Use:   "test",
		Short: "Test a dev container feature",
		Long: `Build a temporary container with a dev container feature installed and
run its test scripts to verify correct behavior.

The command looks for test scenarios in the feature's test/ directory.
Each .sh file in test/ is executed inside the container. If all scripts
exit 0, the feature test passes.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}

	testCmd.Flags().StringVar(
		&cmd.ProjectFolder, "project-folder", "",
		"Path to the feature project directory (containing devcontainer-feature.json)",
	)
	testCmd.Flags().StringVar(
		&cmd.BaseImage, "base-image", "mcr.microsoft.com/devcontainers/base:ubuntu",
		"Base image to install the feature into for testing",
	)
	testCmd.Flags().StringVar(
		&cmd.RemoteUser, "remote-user", "root",
		"User to run tests as inside the container",
	)
	testCmd.Flags().StringVar(
		&cmd.Output, "output", "text", "Output format (text or json)",
	)
	testCmd.Flags().BoolVar(
		&cmd.SkipScenarios, "skip-scenarios", false,
		"Only verify the feature installs successfully without running test scenarios",
	)
	_ = testCmd.MarkFlagRequired("project-folder")

	return testCmd
}

// Run executes the features test workflow.
func (cmd *TestCmd) Run(ctx context.Context) error {
	if err := validateOutputFormat(cmd.Output); err != nil {
		return err
	}

	projectFolder, err := filepath.Abs(cmd.ProjectFolder)
	if err != nil {
		return fmt.Errorf("resolve project folder: %w", err)
	}

	featureCfg, err := config.ParseDevContainerFeature(projectFolder)
	if err != nil {
		return fmt.Errorf("parse feature metadata: %w", err)
	}

	log.Infof("Testing feature %q (v%s)", featureCfg.ID, featureCfg.Version)

	result := cmd.executeTest(ctx, projectFolder, featureCfg)
	return cmd.reportResult(result)
}

func (cmd *TestCmd) executeTest(
	ctx context.Context,
	projectFolder string,
	featureCfg *config.FeatureConfig,
) *testResult {
	result := &testResult{
		FeatureID: featureCfg.ID,
		Passed:    true,
	}

	containerID, err := cmd.buildTestContainer(ctx, projectFolder, featureCfg)
	if err != nil {
		result.Passed = false
		result.Scenarios = append(result.Scenarios, scenarioResult{
			Name:   "install",
			Passed: false,
			Output: err.Error(),
		})
		return result
	}
	defer cmd.cleanupContainer(ctx, containerID)

	result.Scenarios = append(result.Scenarios, scenarioResult{
		Name:   "install",
		Passed: true,
	})

	if cmd.SkipScenarios {
		return result
	}

	cmd.collectScenarios(ctx, containerID, projectFolder, result)
	return result
}

func (cmd *TestCmd) collectScenarios(
	ctx context.Context,
	containerID, projectFolder string,
	result *testResult,
) {
	scenarios, err := cmd.runTestScenarios(ctx, containerID, projectFolder)
	if err != nil {
		result.Passed = false
		result.Scenarios = append(result.Scenarios, scenarioResult{
			Name:   "test-execution",
			Passed: false,
			Output: err.Error(),
		})
		return
	}

	result.Scenarios = append(result.Scenarios, scenarios...)
	for _, s := range scenarios {
		if !s.Passed {
			result.Passed = false
		}
	}
}

func (cmd *TestCmd) buildTestContainer(
	ctx context.Context,
	featureDir string,
	featureCfg *config.FeatureConfig,
) (string, error) {
	dockerfile := generateTestDockerfile(cmd.BaseImage, featureCfg, cmd.RemoteUser)

	tmpDir, err := os.MkdirTemp("", "devsy-feature-test-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	if err := os.WriteFile(
		filepath.Join(tmpDir, "Dockerfile"), []byte(dockerfile), 0o600,
	); err != nil {
		return "", fmt.Errorf("write Dockerfile: %w", err)
	}

	featureDestDir := filepath.Join(tmpDir, "feature")
	if err := devsycopy.Directory(featureDir, featureDestDir); err != nil {
		return "", fmt.Errorf("copy feature to build context: %w", err)
	}

	containerID, err := cmd.dockerBuildAndRun(ctx, tmpDir, featureCfg)
	if err != nil {
		return "", err
	}

	return containerID, nil
}

func (cmd *TestCmd) dockerBuildAndRun(
	ctx context.Context,
	buildContext string,
	featureCfg *config.FeatureConfig,
) (string, error) {
	imageName := fmt.Sprintf("devsy-feature-test-%s:%s", featureCfg.ID, featureCfg.Version)

	log.Infof("Building test container with base image %q", cmd.BaseImage)
	buildArgs := []string{
		"build", "-t", imageName,
		"-f", filepath.Join(buildContext, "Dockerfile"),
		buildContext,
	}

	// #nosec G204 -- args are constructed internally from trusted inputs
	buildCmd := exec.CommandContext(ctx, "docker", buildArgs...)
	var buildOut bytes.Buffer
	buildCmd.Stdout = &buildOut
	buildCmd.Stderr = &buildOut
	if err := buildCmd.Run(); err != nil {
		return "", fmt.Errorf("build test image: %s\n%s", err, buildOut.String())
	}

	log.Infof("Starting test container")
	runArgs := []string{
		"run", "-d",
		"--label", "devsy.feature.test=" + featureCfg.ID,
		imageName,
		"sleep", "infinity",
	}

	// #nosec G204 -- args are constructed internally from trusted inputs
	runCmd := exec.CommandContext(ctx, "docker", runArgs...)
	var runOut bytes.Buffer
	runCmd.Stdout = &runOut
	runCmd.Stderr = &runOut
	if err := runCmd.Run(); err != nil {
		return "", fmt.Errorf("start test container: %s\n%s", err, runOut.String())
	}

	return strings.TrimSpace(runOut.String()), nil
}

func (cmd *TestCmd) runTestScenarios(
	ctx context.Context,
	containerID, featureDir string,
) ([]scenarioResult, error) {
	testDir := filepath.Join(featureDir, "test")
	entries, err := os.ReadDir(testDir)
	if err != nil {
		if os.IsNotExist(err) {
			log.Infof("No test/ directory found, skipping test scenarios")
			return nil, nil
		}
		return nil, fmt.Errorf("read test directory: %w", err)
	}

	var results []scenarioResult
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sh") {
			continue
		}

		scriptPath := filepath.Join(testDir, entry.Name())
		scriptContent, err := os.ReadFile(scriptPath) // #nosec G304 -- user-specified test path
		if err != nil {
			results = append(results, scenarioResult{
				Name:   entry.Name(),
				Passed: false,
				Output: fmt.Sprintf("read script: %v", err),
			})
			continue
		}

		log.Infof("Running test scenario: %s", entry.Name())
		result := cmd.execTestScript(ctx, containerID, entry.Name(), string(scriptContent))
		results = append(results, result)
	}

	return results, nil
}

func (cmd *TestCmd) execTestScript(
	ctx context.Context,
	containerID, name, script string,
) scenarioResult {
	execArgs := []string{
		"exec",
		"-u", cmd.RemoteUser,
		containerID,
		"bash", "-c", script,
	}

	// #nosec G204 -- args are constructed internally from trusted inputs
	execCmd := exec.CommandContext(ctx, "docker", execArgs...)
	var out bytes.Buffer
	execCmd.Stdout = &out
	execCmd.Stderr = &out

	err := execCmd.Run()
	return scenarioResult{
		Name:   name,
		Passed: err == nil,
		Output: out.String(),
	}
}

func (cmd *TestCmd) cleanupContainer(ctx context.Context, containerID string) {
	log.Infof("Cleaning up test container")

	// #nosec G204 -- fixed command with internally-resolved container ID
	rmCmd := exec.CommandContext(ctx, "docker", "rm", "-f", containerID)
	_ = rmCmd.Run()
}

func (cmd *TestCmd) reportResult(result *testResult) error {
	if cmd.Output == outputJSON {
		return writeJSON(os.Stdout, result)
	}

	return cmd.printTextResult(result)
}

func (cmd *TestCmd) printTextResult(result *testResult) error {
	w := os.Stdout

	if result.Passed {
		_, _ = fmt.Fprintf(w, "PASS: Feature %q\n", result.FeatureID)
	} else {
		_, _ = fmt.Fprintf(w, "FAIL: Feature %q\n", result.FeatureID)
	}

	for _, s := range result.Scenarios {
		status := "PASS"
		if !s.Passed {
			status = "FAIL"
		}
		_, _ = fmt.Fprintf(w, "  [%s] %s\n", status, s.Name)
		if !s.Passed && s.Output != "" {
			for line := range strings.SplitSeq(strings.TrimSpace(s.Output), "\n") {
				_, _ = fmt.Fprintf(w, "         %s\n", line)
			}
		}
	}

	if !result.Passed {
		return fmt.Errorf("feature test failed")
	}
	return nil
}

func generateTestDockerfile(
	baseImage string,
	featureCfg *config.FeatureConfig,
	remoteUser string,
) string {
	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s\n", baseImage)
	fmt.Fprintf(&b, "USER %s\n", remoteUser)
	b.WriteString("COPY feature/ /tmp/_dev_container_feature/\n")
	b.WriteString("WORKDIR /tmp/_dev_container_feature\n")

	for optName, opt := range featureCfg.Options {
		envKey := strings.ToUpper(optName)
		defaultVal := string(opt.Default)
		fmt.Fprintf(&b, "ENV %s=%s\n", envKey, defaultVal)
	}

	b.WriteString("RUN chmod +x install.sh && ./install.sh\n")
	b.WriteString("WORKDIR /\n")
	b.WriteString("RUN rm -rf /tmp/_dev_container_feature\n")
	return b.String()
}
