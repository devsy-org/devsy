package crane

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/subprocess"
)

var craneSigningKey string

const (
	PullCommand    = "pull"
	DecryptCommand = "decrypt"

	EnvironmentCrane = "environment"

	defaultBinName    = config.BinaryName + "-crane"
	envDevsyCraneName = config.EnvCraneName
	tmpDirTemplate    = config.BinaryName + "-crane-*"
)

type Content struct {
	Files map[string]string `json:"files"`
}

type command struct {
	cmd  string
	args []string
}

func New(cmd string) *command {
	newCommand := &command{cmd: getBinName()}
	return newCommand.WithArg(cmd)
}

func (c *command) WithFlag(flag, val string) *command {
	c.args = append(c.args, flag, val)
	return c
}

func (c *command) WithArg(arg string) *command {
	c.args = append(c.args, arg)
	return c
}

func (c *command) Run() (string, error) {
	return c.RunContext(context.Background())
}

func (c *command) RunContext(ctx context.Context) (string, error) {
	redactor := secrets.NewEnvironmentRedactor(os.Environ())
	if craneSigningKey != "" {
		redactor = secrets.Combine(
			redactor,
			secrets.NewRedactor([]string{"CRANE_SIGNING_KEY=" + craneSigningKey}),
		)
	}
	result, err := subprocess.Run(ctx, c.cmd, c.args, subprocess.Options{
		Redactor: redactor,
	})
	if err != nil {
		details := result.DiagnosticOutput()
		if details != "" {
			return "", fmt.Errorf("failed to execute command: %s: %w", details, err)
		}
		return "", fmt.Errorf("failed to execute command: %w", err)
	}

	return result.Stdout, nil
}

// ShouldUse takes CLIOptions and returns true if crane should be used.
func ShouldUse(cliOptions *provider2.CLIOptions) bool {
	return IsAvailable() && cliOptions.Platform.EnvironmentTemplate != ""
}

// IsAvailable checks if devsy crane is installed in host system.
func IsAvailable() bool {
	_, err := exec.LookPath(getBinName())
	return err == nil
}

// PullConfigFromSource pulls devcontainer config from configSource using git crane and returns config path.
func PullConfigFromSource(
	workspaceInfo *provider2.AgentWorkspaceInfo,
	options *provider2.CLIOptions,
) (string, error) {
	return PullConfigFromSourceWithContext(context.Background(), workspaceInfo, options)
}

// PullConfigFromSourceWithContext pulls a config using the caller's context.
func PullConfigFromSourceWithContext(
	ctx context.Context,
	workspaceInfo *provider2.AgentWorkspaceInfo,
	options *provider2.CLIOptions,
) (string, error) {
	var data string
	var err error
	if options.Platform.EnvironmentTemplate == "" {
		return "", fmt.Errorf("failed to pull config from source based on options")
	}

	command := New(PullCommand).
		WithArg(EnvironmentCrane).
		WithArg(options.Platform.EnvironmentTemplate)

	if options.Platform.EnvironmentTemplateVersion != "" {
		command = command.WithFlag("--version", options.Platform.EnvironmentTemplateVersion)
	}

	data, err = command.RunContext(ctx)
	if err != nil {
		return "", err
	}

	if craneSigningKey != "" {
		data, err = New(DecryptCommand).WithArg(data).WithFlag("--key", craneSigningKey).RunContext(ctx)
		if err != nil {
			return "", err
		}
	}

	content := &Content{}
	if err := json.Unmarshal([]byte(data), content); err != nil {
		return "", err
	}

	return writeContentToDirectory(workspaceInfo, content)
}

func writeContentToDirectory(
	workspaceInfo *provider2.AgentWorkspaceInfo,
	content *Content,
) (string, error) {
	path := workspaceInfo.ContentFolder
	if path == "" {
		path = createContentDirectory()
		if path == "" {
			return path, fmt.Errorf("failed to create temporary directory")
		}
	}
	return storeFilesInDirectory(content, path)
}

func createContentDirectory() string {
	tmpDir, err := os.MkdirTemp("", tmpDirTemplate)
	if err != nil {
		return ""
	}

	return tmpDir
}

func storeFilesInDirectory(content *Content, path string) (string, error) {
	for filename, fileContent := range content.Files {
		filePath := filepath.Join(path, filename)

		dir := filepath.Dir(filePath)
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			return "", err
		}

		err := os.WriteFile(filePath, []byte(fileContent), os.ModePerm)
		if err != nil {
			_ = os.RemoveAll(path)
			return "", fmt.Errorf("failed to write file %s: %w", filename, err)
		}
	}

	return path, nil
}

func getBinName() string {
	if name := os.Getenv(envDevsyCraneName); name != "" {
		return name
	}
	return defaultBinName
}
