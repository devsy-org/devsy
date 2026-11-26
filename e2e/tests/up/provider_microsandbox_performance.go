package up

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/provider"
)

const (
	spawnProbeEnv       = "DEVSY_TEST_RUNTIME_SPAWN_PROBE"
	supervisorEnv       = "DEVSY_TEST_RUNTIME_SUPERVISOR"
	performanceDir      = "DEVSY_TEST_RUNTIME_PERFORMANCE_DIR"
	revisionEnv         = "DEVSY_TEST_RUNTIME_REVISION"
	sdkVersionEnv       = "DEVSY_TEST_RUNTIME_SDK_VERSION"
	performanceFile     = "provider-performance.json"
	runtimeSDKModule    = "github.com/devsy-org/devsy-runtime-sdk"
	probeModeDirect     = "direct"
	probeModeSupervised = "supervised"
	performanceUpFresh  = "fresh"
	performanceUpReused = "reused"
)

var startupEvent = regexp.MustCompile(`External runtime ([^ ]+) startup completed in ([^\s]+)`)

type microsandboxPerformanceCapture struct {
	active        bool
	probe         string
	supervisor    string
	directory     string
	config        *provider.ProviderConfig
	framework     *framework.Framework
	workspace     *provider.Workspace
	options       map[string]config.OptionValue
	upRuns        []upPerformanceRun
	probeRuns     []probePerformanceRun
	expectedSHA   string
	supervisorSHA string
	revision      string
	sdkVersion    string
}

type performanceToolPaths struct {
	probe, supervisor, directory, revision, sdkVersion string
}

type performanceSDKTool struct {
	name, path, packagePath string
}

type stagedPerformanceRuntime struct {
	name, path, checksum string
}

type probeSequence struct {
	environment []string
	runtime     stagedPerformanceRuntime
	workspaceID string
}

type probeInvocation struct {
	sequence probeSequence
	order    int
	mode     string
}

type upPerformanceRun struct {
	Phase      string               `json:"phase"`
	Succeeded  bool                 `json:"succeeded"`
	WallTimeNS int64                `json:"wall_time_ns"`
	Startup    []startupPerformance `json:"startup_events"`
}

type startupPerformance struct {
	Operation  string `json:"operation"`
	DurationNS int64  `json:"duration_ns"`
}

type probePerformanceRun struct {
	Order  int             `json:"order"`
	Mode   string          `json:"launch_mode"`
	Report json.RawMessage `json:"report"`
}

type microsandboxPerformanceReport struct {
	ProviderSource         string                `json:"provider_source"`
	ExpectedBinarySHA256   string                `json:"expected_binary_sha256"`
	TestedCheckoutRevision string                `json:"tested_checkout_revision"`
	RuntimeSDKVersion      string                `json:"runtime_sdk_version"`
	StartupMeasurement     string                `json:"startup_measurement"`
	UpRuns                 []upPerformanceRun    `json:"up_runs"`
	ProbeRuns              []probePerformanceRun `json:"probe_runs"`
}

// newMicrosandboxPerformanceCapture enables the optional measurements only for
// the published external provider and only when all native-job paths exist.
func newMicrosandboxPerformanceCapture(
	source, providerName string,
	f *framework.Framework,
) (*microsandboxPerformanceCapture, error) {
	capture := &microsandboxPerformanceCapture{framework: f}
	paths, active, err := configuredPerformanceTools(source)
	if err != nil || !active {
		return capture, err
	}
	config, err := installedExternalPerformanceProvider(providerName, source)
	if err != nil {
		return nil, err
	}
	capture.active = true
	capture.probe = paths.probe
	capture.supervisor = paths.supervisor
	capture.directory = paths.directory
	capture.config = config
	capture.revision = paths.revision
	capture.sdkVersion = paths.sdkVersion
	capture.supervisorSHA, err = fileSHA256(paths.supervisor)
	if err != nil {
		return nil, fmt.Errorf("hash SDK runtime supervisor: %w", err)
	}
	return capture, nil
}

func configuredPerformanceTools(source string) (performanceToolPaths, bool, error) {
	if source != microsandboxExternalProvider {
		return performanceToolPaths{}, false, nil
	}
	paths := performanceToolPaths{
		probe: os.Getenv(spawnProbeEnv), supervisor: os.Getenv(supervisorEnv),
		directory: os.Getenv(performanceDir), revision: os.Getenv(revisionEnv),
		sdkVersion: os.Getenv(sdkVersionEnv),
	}
	set := 0
	for _, value := range []string{
		paths.probe, paths.supervisor, paths.directory, paths.revision, paths.sdkVersion,
	} {
		if value != "" {
			set++
		}
	}
	if set == 0 {
		return performanceToolPaths{}, false, nil
	}
	if set != 5 {
		return performanceToolPaths{}, false, errors.New(
			"MicroSandbox performance tools are only partially configured",
		)
	}
	if runtime.GOOS != osLinux {
		return performanceToolPaths{}, false, errors.New(
			"MicroSandbox real-provider performance capture requires Linux",
		)
	}
	if err := validatePerformanceToolPaths(paths); err != nil {
		return performanceToolPaths{}, false, err
	}
	return paths, true, nil
}

func validatePerformanceToolPaths(paths performanceToolPaths) error {
	if err := validatePerformanceSDKTools(paths); err != nil {
		return err
	}
	return validatePerformanceReportDirectory(paths.directory)
}

func validatePerformanceSDKTools(paths performanceToolPaths) error {
	for _, tool := range []performanceSDKTool{
		{spawnProbeEnv, paths.probe, runtimeSDKModule + "/cmd/devsy-runtime-spawn-probe"},
		{supervisorEnv, paths.supervisor, runtimeSDKModule + "/cmd/devsy-runtime-supervisor"},
	} {
		if err := validatePerformanceSDKTool(tool, paths.sdkVersion); err != nil {
			return err
		}
	}
	return nil
}

func validatePerformanceSDKTool(tool performanceSDKTool, version string) error {
	if !filepath.IsAbs(tool.path) {
		return fmt.Errorf("%s must be an absolute path", tool.name)
	}
	info, err := os.Stat(tool.path)
	if err != nil {
		return fmt.Errorf("%s is unavailable: %w", tool.name, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s must name an executable file", tool.name)
	}
	return validateSDKTool(tool.path, tool.packagePath, version)
}

func validatePerformanceReportDirectory(directory string) error {
	if !filepath.IsAbs(directory) {
		return errors.New("performance report directory must be absolute")
	}
	// #nosec G703 -- this absolute report path is supplied by the trusted native CI job.
	dirInfo, err := os.Stat(directory)
	if err != nil {
		return fmt.Errorf("performance report directory is unavailable: %w", err)
	}
	if !dirInfo.IsDir() {
		return errors.New("performance report path is not a directory")
	}
	return nil
}

func installedExternalPerformanceProvider(
	providerName, source string,
) (*provider.ProviderConfig, error) {
	installed, err := provider.LoadProviderConfig(config.DefaultContext, providerName)
	if err != nil {
		return nil, fmt.Errorf("load installed MicroSandbox provider configuration: %w", err)
	}
	if installed.Source.Raw != source || installed.Agent.Driver != provider.ExternalDriver ||
		installed.Agent.External.Binary == "" {
		return nil, errors.New(
			"installed provider does not match the expected external MicroSandbox source",
		)
	}
	return installed, nil
}

func validateSDKTool(path, packagePath, version string) error {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read runtime probe tool build information: %w", err)
	}
	if info.Path != packagePath || info.Main.Path != runtimeSDKModule ||
		info.Main.Version != version ||
		info.Main.Replace != nil {
		return fmt.Errorf("runtime probe tool is not the expected unmodified SDK %s build", version)
	}
	return nil
}

func (c *microsandboxPerformanceCapture) up(
	ctx context.Context,
	workspace, phase string,
	additionalArgs ...string,
) error {
	if !c.active {
		args := append([]string{workspace}, additionalArgs...)
		return c.framework.DevsyUp(ctx, args...)
	}
	if err := c.validateUpPhase(phase); err != nil {
		return err
	}
	_, err := c.captureUp(ctx, workspace, phase, additionalArgs...)
	if err != nil {
		return err
	}
	if phase == performanceUpFresh {
		return c.captureFreshWorkspace(ctx, workspace)
	}
	return nil
}

func (c *microsandboxPerformanceCapture) validateUpPhase(phase string) error {
	if phase != performanceUpFresh && phase != performanceUpReused {
		return fmt.Errorf("unsupported MicroSandbox performance up phase %q", phase)
	}
	if phase == performanceUpFresh && len(c.upRuns) == 0 {
		return nil
	}
	if phase == performanceUpReused && len(c.upRuns) == 1 &&
		c.upRuns[0].Phase == performanceUpFresh {
		return nil
	}
	return fmt.Errorf("MicroSandbox performance up phase %q is out of order", phase)
}

func (c *microsandboxPerformanceCapture) captureUp(
	ctx context.Context,
	workspace, phase string,
	additionalArgs ...string,
) (upPerformanceRun, error) {
	started := time.Now()
	stdout, stderr, err := c.framework.DevsyUpStreams(ctx, workspace, additionalArgs...)
	run := upPerformanceRun{
		Phase:      phase,
		Succeeded:  err == nil,
		WallTimeNS: time.Since(started).Nanoseconds(),
	}
	run.Startup = append(run.Startup, parseStartupEvents(stdout)...)
	run.Startup = append(run.Startup, parseStartupEvents(stderr)...)
	c.upRuns = append(c.upRuns, run)
	if writeErr := c.writeReport(); writeErr != nil {
		return run, errors.Join(
			err,
			fmt.Errorf("write MicroSandbox performance report: %w", writeErr),
		)
	}
	if err != nil {
		return run, err
	}
	if len(run.Startup) == 0 {
		return run, errors.New(
			"successful MicroSandbox up emitted no valid external runtime startup timing events",
		)
	}
	return run, nil
}

func (c *microsandboxPerformanceCapture) captureFreshWorkspace(
	ctx context.Context,
	workspace string,
) error {
	var err error
	c.workspace, err = c.framework.FindWorkspace(ctx, workspace)
	if err != nil {
		return fmt.Errorf("find fresh MicroSandbox workspace for runtime probe: %w", err)
	}
	devsyConfig, err := config.LoadConfig("", "")
	if err != nil {
		return fmt.Errorf("load options for runtime probe environment: %w", err)
	}
	c.options = provider.GetProviderOptions(c.workspace, nil, devsyConfig)
	return nil
}

func parseStartupEvents(output string) []startupPerformance {
	var events []startupPerformance
	for line := range strings.SplitSeq(output, "\n") {
		match := startupEvent.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		duration, err := time.ParseDuration(match[2])
		if err == nil && duration > 0 {
			events = append(
				events,
				startupPerformance{Operation: match[1], DurationNS: duration.Nanoseconds()},
			)
		}
	}
	return events
}

func (c *microsandboxPerformanceCapture) compare(ctx context.Context) (retErr error) {
	if !c.active {
		return nil
	}
	if !c.capturesReady() {
		return errors.New(
			"runtime comparison requires successful fresh and reused provider up captures",
		)
	}
	stage, err := os.MkdirTemp("", "devsy-runtime-performance-")
	if err != nil {
		return fmt.Errorf("create private runtime staging directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(stage); err != nil {
			retErr = errors.Join(
				retErr,
				fmt.Errorf("remove private runtime staging directory: %w", err),
			)
		}
	}()
	return c.compareWithStage(ctx, stage)
}

func (c *microsandboxPerformanceCapture) capturesReady() bool {
	return len(c.upRuns) == 2 && c.upRuns[0].Phase == performanceUpFresh &&
		c.upRuns[0].Succeeded && c.upRuns[1].Phase == performanceUpReused &&
		c.upRuns[1].Succeeded && c.workspace != nil && c.options != nil
}

func (c *microsandboxPerformanceCapture) compareWithStage(ctx context.Context, stage string) error {
	paths, err := provider.DownloadBinaries(ctx, c.config.Agent.Binaries, stage)
	if err != nil {
		return fmt.Errorf("stage published external runtime from Agent.Binaries: %w", err)
	}
	staged, err := c.validateStagedRuntime(paths)
	if err != nil {
		return err
	}
	c.expectedSHA = staged.checksum
	workspaceID, err := absentWorkspaceID()
	if err != nil {
		return err
	}
	probeEnv := provider.ToEnvironment(
		c.workspace,
		nil,
		c.options,
		map[string]string{staged.name: staged.path},
	)
	return c.runProbeSequence(ctx, probeSequence{
		environment: probeEnv, runtime: staged, workspaceID: workspaceID,
	})
}

func (c *microsandboxPerformanceCapture) validateStagedRuntime(
	paths map[string]string,
) (stagedPerformanceRuntime, error) {
	binaryName := c.config.Agent.External.Binary
	binaryPath := paths[binaryName]
	if binaryPath == "" || !filepath.IsAbs(binaryPath) {
		return stagedPerformanceRuntime{}, errors.New(
			"published external runtime did not resolve to an absolute staged binary",
		)
	}
	if err := validateStagedExecutable(binaryPath); err != nil {
		return stagedPerformanceRuntime{}, err
	}
	expected, err := selectedRuntimeChecksum(c.config, binaryName)
	if err != nil {
		return stagedPerformanceRuntime{}, err
	}
	actual, err := fileSHA256(binaryPath)
	if err != nil {
		return stagedPerformanceRuntime{}, fmt.Errorf("hash staged external runtime: %w", err)
	}
	if actual != expected {
		return stagedPerformanceRuntime{}, fmt.Errorf(
			"staged external runtime checksum differs from provider manifest: got %s",
			actual,
		)
	}
	return stagedPerformanceRuntime{name: binaryName, path: binaryPath, checksum: expected}, nil
}

func validateStagedExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat staged external runtime: %w", err)
	}
	if info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
		return nil
	}
	return errors.New("staged external runtime is not an executable regular file")
}

func (c *microsandboxPerformanceCapture) runProbeSequence(
	ctx context.Context,
	sequence probeSequence,
) error {
	for index, mode := range []string{
		probeModeDirect, probeModeSupervised, probeModeSupervised, probeModeDirect,
	} {
		invocation := probeInvocation{sequence: sequence, order: index + 1, mode: mode}
		if err := c.runProbe(ctx, invocation); err != nil {
			return err
		}
	}
	return nil
}

func (c *microsandboxPerformanceCapture) runProbe(
	ctx context.Context,
	invocation probeInvocation,
) error {
	sequence := invocation.sequence
	args := []string{
		"--binary", sequence.runtime.path, "--samples", "100", "--timeout", "15s",
		"--workspace-id", sequence.workspaceID, "--revision", c.revision,
	}
	if invocation.mode == probeModeSupervised {
		args = append(args, "--supervisor-binary", c.supervisor)
	}
	args = append(args, "--")
	args = append(args, c.config.Agent.External.Args...)
	// #nosec G204 -- SDK tools and runtime checksum are verified; arguments are fixed flags plus manifest argv.
	command := exec.CommandContext(ctx, c.probe, args...)
	docker.PrepareForGroupCancellation(command)
	command.WaitDelay = 5 * time.Second
	command.Env = sequence.environment
	var stdout strings.Builder
	command.Stdout = &stdout
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return fmt.Errorf(
			"runtime spawn probe run %d (%s) failed: %w", invocation.order, invocation.mode, err,
		)
	}
	raw := []byte(stdout.String())
	expectation := probeExpectations{
		mode: invocation.mode, binarySHA: sequence.runtime.checksum,
		supervisorSHA: c.supervisorSHA, revision: c.revision,
	}
	if err := expectation.validate(raw); err != nil {
		return fmt.Errorf(
			"runtime spawn probe run %d (%s): %w", invocation.order, invocation.mode, err,
		)
	}
	c.probeRuns = append(
		c.probeRuns,
		probePerformanceRun{
			Order: invocation.order, Mode: invocation.mode, Report: json.RawMessage(raw),
		},
	)
	if err := c.writeReport(); err != nil {
		return fmt.Errorf("write MicroSandbox performance report: %w", err)
	}
	return nil
}

func selectedRuntimeChecksum(config *provider.ProviderConfig, name string) (string, error) {
	for _, binary := range config.Agent.Binaries[name] {
		if binary == nil {
			continue
		}
		if binary.OS == runtime.GOOS && binary.Arch == runtime.GOARCH {
			checksum := strings.TrimPrefix(strings.ToLower(binary.Checksum), "sha256:")
			decoded, err := hex.DecodeString(checksum)
			if err != nil || len(decoded) != sha256.Size {
				return "", errors.New("provider runtime manifest has an invalid SHA-256 checksum")
			}
			return checksum, nil
		}
	}
	return "", fmt.Errorf(
		"provider runtime manifest has no %s/%s entry",
		runtime.GOOS,
		runtime.GOARCH,
	)
}

type runtimeProbeSample struct {
	Reaped bool `json:"reaped"`
}

type runtimeProbeMeasurements struct {
	Operation string               `json:"operation"`
	First     runtimeProbeSample   `json:"first_launch"`
	Warm      []runtimeProbeSample `json:"warm_samples"`
}

type runtimeProbeReport struct {
	SchemaVersion     int                      `json:"schema_version"`
	LaunchMode        string                   `json:"launch_mode"`
	SupervisorSHA256  string                   `json:"supervisor_sha256"`
	GOOS              string                   `json:"goos"`
	GOARCH            string                   `json:"goarch"`
	GoVersion         string                   `json:"go_version"`
	Revision          string                   `json:"revision"`
	BinarySHA256      string                   `json:"binary_sha256"`
	ColdCacheMeasured *bool                    `json:"cold_cache_measured"`
	Info              runtimeProbeMeasurements `json:"info"`
	Find              runtimeProbeMeasurements `json:"find"`
}

type probeExpectations struct {
	mode, binarySHA, supervisorSHA, revision string
}

func (expected probeExpectations) validate(raw []byte) error {
	var report runtimeProbeReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return fmt.Errorf("decode probe JSON: %w", err)
	}
	if err := report.validateMetadata(expected); err != nil {
		return err
	}
	if err := validateMeasurements("Info", report.Info); err != nil {
		return err
	}
	return validateMeasurements("Find", report.Find)
}

func (report runtimeProbeReport) validateMetadata(expected probeExpectations) error {
	if err := report.validateLaunch(expected); err != nil {
		return err
	}
	if err := report.validatePlatform(expected); err != nil {
		return err
	}
	return report.validateSupervisor(expected)
}

func (report runtimeProbeReport) validateLaunch(expected probeExpectations) error {
	if expected.mode != probeModeDirect && expected.mode != probeModeSupervised {
		return errors.New("probe launch mode is unsupported")
	}
	if report.SchemaVersion != 1 || report.LaunchMode != expected.mode ||
		report.BinarySHA256 != expected.binarySHA || report.ColdCacheMeasured == nil ||
		*report.ColdCacheMeasured {
		return errors.New(
			"probe report does not match requested launch mode, expected binary, or cache claim",
		)
	}
	return nil
}

func (report runtimeProbeReport) validatePlatform(expected probeExpectations) error {
	if report.GOOS == runtime.GOOS && report.GOARCH == runtime.GOARCH &&
		report.GoVersion != "" && report.Revision == expected.revision {
		return nil
	}
	return errors.New(
		"probe report platform or tested checkout revision does not match the current run",
	)
}

func (report runtimeProbeReport) validateSupervisor(expected probeExpectations) error {
	wantSupervisorSHA := ""
	if expected.mode == probeModeSupervised {
		wantSupervisorSHA = expected.supervisorSHA
	}
	if report.SupervisorSHA256 != wantSupervisorSHA {
		return errors.New(
			"probe report supervisor checksum does not match the selected launch mode",
		)
	}
	return nil
}

func validateMeasurements(label string, measurement runtimeProbeMeasurements) error {
	if measurement.Operation != label || !measurement.First.Reaped || len(measurement.Warm) != 100 {
		return fmt.Errorf(
			"%s report is missing the expected first launch or 100 warm samples",
			label,
		)
	}
	for _, sample := range measurement.Warm {
		if !sample.Reaped {
			return fmt.Errorf("%s report includes a process that was not reaped", label)
		}
	}
	return nil
}

func (c *microsandboxPerformanceCapture) writeReport() error {
	report := microsandboxPerformanceReport{
		ProviderSource:         microsandboxExternalProvider,
		ExpectedBinarySHA256:   c.expectedSHA,
		TestedCheckoutRevision: c.revision,
		RuntimeSDKVersion:      c.sdkVersion,
		StartupMeasurement:     "external runtime startup event; excludes binary resolution, RPC, and process reaping",
		UpRuns:                 c.upRuns,
		ProbeRuns:              c.probeRuns,
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(c.directory, performanceFile), append(data, '\n'), 0o600)
}

func fileSHA256(path string) (string, error) {
	// #nosec G304 -- caller selects only the validated SDK supervisor or downloaded manifest binary in private staging.
	// #nosec G703 -- absolute SDK path is CI-validated; runtime path is the manifest download in private staging.
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func absentWorkspaceID() (string, error) {
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generate absent workspace ID: %w", err)
	}
	return "runtime-probe-" + hex.EncodeToString(suffix[:]), nil
}
