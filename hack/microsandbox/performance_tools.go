package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/blang/semver/v4"
)

const runtimeSDKModulePath = "github.com/devsy-org/devsy-runtime-sdk"

var runtimeSDKTools = []string{
	"devsy-runtime-spawn-probe",
	"devsy-runtime-supervisor",
}

type runtimeSDKModule struct {
	Path    string                 `json:"Path"`
	Version string                 `json:"Version"`
	Replace *runtimeSDKReplacement `json:"Replace"`
}

type runtimeSDKReplacement struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
}

func buildPerformanceToolsCLI(args []string, stdout, stderr io.Writer) int {
	if runtime.GOOS != "linux" {
		_, _ = fmt.Fprintln(stderr, "build-performance-tools requires Linux")
		return 2
	}
	flags := flag.NewFlagSet(buildPerformanceToolsCommand, flag.ContinueOnError)
	flags.SetOutput(stderr)
	outputDir := flags.String("output-dir", "", "absolute directory for SDK tools")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || !filepath.IsAbs(*outputDir) {
		_, _ = fmt.Fprintln(
			stderr,
			"build-performance-tools requires an absolute --output-dir and no positional arguments",
		)
		return 2
	}
	err := buildPerformanceTools(
		context.Background(), *outputDir, commandRunner((*exec.Cmd).Run),
		commandOutput{stdout: stdout, stderr: stderr},
	)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "::error::%v\n", err)
		return commandExitCode(err)
	}
	return 0
}

func buildPerformanceTools(
	ctx context.Context,
	outputDir string,
	run commandRunner,
	output commandOutput,
) error {
	output = normalizedCommandOutput(output)
	if !filepath.IsAbs(outputDir) {
		return errors.New("performance tool output directory must be absolute")
	}
	if run == nil {
		return errors.New("command runner is required")
	}
	metadataPath, err := preparePerformanceToolOutput(outputDir)
	if err != nil {
		return err
	}

	environment := replaceCommandEnvironment(os.Environ(), map[string]string{
		"GOBIN": outputDir, "CGO_ENABLED": "0",
	})
	version, err := runtimeSDKVersion(ctx, run, environment, output.stderr)
	if err != nil {
		return err
	}
	for _, tool := range runtimeSDKTools {
		module := runtimeSDKModulePath + "/cmd/" + tool + "@" + version
		cmd := exec.CommandContext(ctx, "go", "install", "-trimpath", module)
		cmd.Env = environment
		cmd.Stdout = output.stdout
		cmd.Stderr = output.stderr
		if err := run(cmd); err != nil {
			return fmt.Errorf("build pinned SDK tool %s: %w", tool, err)
		}
	}
	if err := writeRuntimeSDKVersion(metadataPath, version); err != nil {
		return fmt.Errorf("write SDK version metadata: %w", err)
	}
	_, err = fmt.Fprintf(
		output.stdout,
		"built runtime SDK tools at %s for %s\n",
		outputDir,
		version,
	)
	return err
}

func preparePerformanceToolOutput(outputDir string) (string, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("create performance tool output directory: %w", err)
	}
	info, err := os.Stat(outputDir)
	if err != nil {
		return "", fmt.Errorf("inspect performance tool output directory: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("performance tool output path must be a directory")
	}
	metadataPath := filepath.Join(outputDir, "sdk-version")
	if err := os.Remove(metadataPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("remove stale SDK version metadata: %w", err)
	}
	return metadataPath, nil
}

func runtimeSDKVersion(
	ctx context.Context,
	run commandRunner,
	environment []string,
	stderr io.Writer,
) (string, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-json", runtimeSDKModulePath)
	cmd.Env = environment
	var stdout strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = stderr
	if err := run(cmd); err != nil {
		return "", fmt.Errorf("resolve pinned runtime SDK module: %w", err)
	}
	var module runtimeSDKModule
	if err := json.Unmarshal([]byte(stdout.String()), &module); err != nil {
		return "", fmt.Errorf("decode runtime SDK module metadata: %w", err)
	}
	_, versionErr := semver.Parse(strings.TrimPrefix(module.Version, "v"))
	if module.Path != runtimeSDKModulePath || !strings.HasPrefix(module.Version, "v") ||
		versionErr != nil || module.Replace != nil {
		return "", errors.New("runtime SDK must resolve to its pinned version without a replace")
	}
	return module.Version, nil
}

func replaceCommandEnvironment(environment []string, replacements map[string]string) []string {
	result := make([]string, 0, len(environment)+len(replacements))
	for _, entry := range environment {
		key, _, ok := strings.Cut(entry, "=")
		if _, replace := replacements[key]; !ok || !replace {
			result = append(result, entry)
		}
	}
	for key, value := range replacements {
		result = append(result, key+"="+value)
	}
	return result
}

func writeRuntimeSDKVersion(path, version string) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".sdk-version-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer func() {
		if removeErr := os.Remove(
			tempPath,
		); removeErr != nil &&
			!errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	if err = file.Chmod(0o644); err != nil {
		return errors.Join(err, file.Close())
	}
	if _, err = io.WriteString(file, version+"\n"); err != nil {
		return errors.Join(err, file.Close())
	}
	if err = file.Sync(); err != nil {
		return errors.Join(err, file.Close())
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
