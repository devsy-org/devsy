package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/compress"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

const DefaultInactivityTimeout = time.Minute * 20

func DecodeContainerWorkspaceInfo(
	workspaceInfoRaw string,
) (*provider2.ContainerWorkspaceInfo, string, error) {
	decoded, err := compress.Decompress(workspaceInfoRaw)
	if err != nil {
		return nil, "", fmt.Errorf("decode workspace info: %w", err)
	}

	workspaceInfo := &provider2.ContainerWorkspaceInfo{}
	err = json.Unmarshal([]byte(decoded), workspaceInfo)
	if err != nil {
		return nil, "", fmt.Errorf("parse workspace info: %w", err)
	}

	return workspaceInfo, decoded, nil
}

func DecodeWorkspaceInfo(workspaceInfoRaw string) (*provider2.AgentWorkspaceInfo, string, error) {
	decoded, err := compress.Decompress(workspaceInfoRaw)
	if err != nil {
		return nil, "", fmt.Errorf("decode workspace info: %w", err)
	}

	workspaceInfo := &provider2.AgentWorkspaceInfo{}
	err = json.Unmarshal([]byte(decoded), workspaceInfo)
	if err != nil {
		return nil, "", fmt.Errorf("parse workspace info: %w", err)
	}

	return workspaceInfo, decoded, nil
}

func readAgentWorkspaceInfo(
	agentFolder, context, id string,
) (*provider2.AgentWorkspaceInfo, error) {
	// get workspace folder
	workspaceDir, err := GetAgentWorkspaceDir(agentFolder, context, id)
	if err != nil {
		return nil, err
	}

	// parse agent workspace info
	return ParseAgentWorkspaceInfo(filepath.Join(workspaceDir, provider2.WorkspaceConfigFile))
}

func ParseAgentWorkspaceInfo(workspaceConfigFile string) (*provider2.AgentWorkspaceInfo, error) {
	// read workspace config
	out, err := os.ReadFile(workspaceConfigFile)
	if err != nil {
		return nil, err
	}

	// json unmarshal
	workspaceInfo := &provider2.AgentWorkspaceInfo{}
	err = json.Unmarshal(out, workspaceInfo)
	if err != nil {
		return nil, fmt.Errorf("parse workspace info: %w", err)
	}

	workspaceInfo.Origin = filepath.Dir(workspaceConfigFile)
	return workspaceInfo, nil
}

func ReadAgentWorkspaceInfo(
	agentFolder, context, id string,
) (bool, *provider2.AgentWorkspaceInfo, error) {
	log.Debugf(
		"starting to read agent workspace info: agentFolder=%s, context=%s, workspaceId=%s",
		agentFolder,
		context,
		id,
	)

	workspaceInfo, err := readAgentWorkspaceInfo(agentFolder, context, id)
	if err != nil && !errors.Is(err, ErrFindAgentHomeFolder) && !errors.Is(err, os.ErrPermission) {
		log.Errorf(
			"failed to read agent workspace info: error=%v, agentFolder=%s, context=%s, workspaceId=%s",
			err,
			agentFolder,
			context,
			id,
		)
		return false, nil, err
	}

	if errors.Is(err, ErrFindAgentHomeFolder) {
		log.Debugf(
			"agent home folder not found: agentFolder=%s, context=%s, workspaceId=%s",
			agentFolder,
			context,
			id,
		)
	}

	if errors.Is(err, os.ErrPermission) {
		log.Debugf(
			"permission denied reading workspace info: agentFolder=%s, context=%s, workspaceId=%s",
			agentFolder,
			context,
			id,
		)
	}

	// check if we need to become root
	log.Debug("checking if root privileges are required")
	shouldExit, err := rerunAsRoot(workspaceInfo)
	if err != nil {
		log.Errorf("failed to rerun as root: error=%v", err)
		return false, nil, fmt.Errorf("rerun as root: %w", err)
	} else if shouldExit {
		log.Debug("rerunning as root, exiting current process")
		return true, nil, nil
	} else if workspaceInfo == nil {
		log.Debug("no workspace info available and not rerunning as root")
		return false, nil, ErrFindAgentHomeFolder
	}

	log.Debugf(
		"read agent workspace info: workspaceId=%s, driver=%s",
		workspaceInfo.Workspace.ID,
		workspaceInfo.Agent.Driver,
	)
	return false, workspaceInfo, nil
}

func WorkspaceInfo(
	workspaceInfoEncoded string,
) (bool, *provider2.AgentWorkspaceInfo, error) {
	return decodeWorkspaceInfoAndWrite(workspaceInfoEncoded, false, nil)
}

func WriteWorkspaceInfo(
	workspaceInfoEncoded string,
) (bool, *provider2.AgentWorkspaceInfo, error) {
	return WriteWorkspaceInfoAndDeleteOld(workspaceInfoEncoded, nil)
}

func WriteWorkspaceInfoAndDeleteOld(
	workspaceInfoEncoded string,
	deleteWorkspace func(workspaceInfo *provider2.AgentWorkspaceInfo) error,
) (bool, *provider2.AgentWorkspaceInfo, error) {
	return decodeWorkspaceInfoAndWrite(workspaceInfoEncoded, true, deleteWorkspace)
}

func decodeWorkspaceInfoAndWrite(
	workspaceInfoEncoded string,
	writeInfo bool,
	deleteWorkspace func(workspaceInfo *provider2.AgentWorkspaceInfo) error,
) (bool, *provider2.AgentWorkspaceInfo, error) {
	workspaceInfo, _, err := DecodeWorkspaceInfo(workspaceInfoEncoded)
	if err != nil {
		return false, nil, err
	}

	if shouldExit, err := rerunAsRoot(workspaceInfo); err != nil {
		return false, nil, fmt.Errorf("rerun as root: %w", err)
	} else if shouldExit {
		return true, nil, nil
	}

	workspaceDir, err := CreateAgentWorkspaceDir(
		workspaceInfo.Agent.DataPath,
		workspaceInfo.Workspace.Context,
		workspaceInfo.Workspace.ID,
	)
	if err != nil {
		return false, nil, fmt.Errorf("create workspace dir: %w", err)
	}

	workspaceConfig := filepath.Join(workspaceDir, provider2.WorkspaceConfigFile)
	if deleteWorkspace != nil {
		workspaceDir, err = handleStaleWorkspace(
			workspaceInfo, workspaceDir, workspaceConfig, deleteWorkspace,
		)
		if err != nil {
			return false, nil, err
		}
	}

	resolveContentFolder(workspaceInfo, workspaceDir)

	if writeInfo {
		if err := writeWorkspaceInfo(workspaceConfig, workspaceInfo); err != nil {
			return false, nil, fmt.Errorf("write workspace info: %w", err)
		}
	}

	workspaceInfo.Origin = workspaceDir
	return false, workspaceInfo, nil
}

// handleStaleWorkspace deletes a workspace whose persisted UID no longer
// matches the incoming one, then recreates the workspace dir. Returns the
// (possibly new) workspaceDir.
func handleStaleWorkspace(
	workspaceInfo *provider2.AgentWorkspaceInfo,
	workspaceDir, workspaceConfig string,
	deleteWorkspace func(*provider2.AgentWorkspaceInfo) error,
) (string, error) {
	oldWorkspaceInfo, _ := ParseAgentWorkspaceInfo(workspaceConfig)
	if oldWorkspaceInfo == nil ||
		oldWorkspaceInfo.Workspace.UID == workspaceInfo.Workspace.UID {
		return workspaceDir, nil
	}

	log.Infof(
		"delete old workspace: workspaceId=%s, oldUid=%s, newUid=%s",
		oldWorkspaceInfo.Workspace.ID,
		oldWorkspaceInfo.Workspace.UID,
		workspaceInfo.Workspace.UID,
	)
	if err := deleteWorkspace(oldWorkspaceInfo); err != nil {
		return "", fmt.Errorf("delete old workspace: %w", err)
	}

	newDir, err := CreateAgentWorkspaceDir(
		workspaceInfo.Agent.DataPath,
		workspaceInfo.Workspace.Context,
		workspaceInfo.Workspace.ID,
	)
	if err != nil {
		return "", fmt.Errorf("recreate workspace dir: %w", err)
	}

	// Drop the old UID's path so resolveContentFolder picks up the new one.
	workspaceInfo.ContentFolder = ""
	return newDir, nil
}

// resolveContentFolder fills in workspaceInfo.ContentFolder, preferring a
// LocalFolder source when accessible. Skipped under Platform.Enabled because
// the host's local folder path is not addressable from the managed runner.
func resolveContentFolder(
	workspaceInfo *provider2.AgentWorkspaceInfo,
	workspaceDir string,
) {
	if workspaceInfo.Workspace.Source.LocalFolder != "" &&
		!workspaceInfo.CLIOptions.Platform.Enabled {
		if _, err := os.Stat(workspaceInfo.WorkspaceOrigin); err == nil {
			workspaceInfo.ContentFolder = workspaceInfo.Workspace.Source.LocalFolder
		}
	}
	if workspaceInfo.ContentFolder == "" {
		workspaceInfo.ContentFolder = GetAgentWorkspaceContentDir(
			workspaceDir,
			workspaceInfo.Workspace.UID,
		)
	}
}

func CreateWorkspaceBusyFile(folder string) {
	filePath := filepath.Join(folder, config.WorkspaceBusyFile)
	_, err := os.Stat(filePath)
	if err == nil {
		return
	}

	_ = os.WriteFile(filePath, nil, 0o600)
}

func HasWorkspaceBusyFile(folder string) bool {
	filePath := filepath.Join(folder, config.WorkspaceBusyFile)
	_, err := os.Stat(filePath)
	return err == nil
}

func DeleteWorkspaceBusyFile(folder string) {
	_ = os.Remove(filepath.Join(folder, config.WorkspaceBusyFile))
}

func writeWorkspaceInfo(file string, workspaceInfo *provider2.AgentWorkspaceInfo) error {
	// copy workspace info
	cloned := provider2.CloneAgentWorkspaceInfo(workspaceInfo)

	// never save cli options
	cloned.CLIOptions = provider2.CLIOptions{}

	// encode workspace info
	encoded, err := json.Marshal(workspaceInfo)
	if err != nil {
		return err
	}

	// write workspace config
	err = provider2.WriteFileAtomic(file, encoded, 0o600)
	if err != nil {
		return fmt.Errorf("write workspace config file: %w", err)
	}

	return nil
}

func rerunAsRoot(workspaceInfo *provider2.AgentWorkspaceInfo) (bool, error) {
	// check if root is required
	if runtime.GOOS != "linux" || os.Getuid() == 0 ||
		(workspaceInfo != nil && workspaceInfo.Agent.Local == config.BoolTrue) {
		return false, nil
	}

	dockerRootRequired := isDockerRootRequired(workspaceInfo)

	// check if daemon needs to be installed
	agentRootRequired := workspaceInfo == nil || len(workspaceInfo.Agent.Exec.Shutdown) > 0

	// check if root required
	if !dockerRootRequired && !agentRootRequired {
		log.Debug("no root required, because neither docker nor agent daemon needs to be installed")
		return false, nil
	}

	// execute ourself as root
	binary, err := os.Executable()
	if err != nil {
		return false, err
	}

	// call ourself
	args := []string{"--preserve-env", binary}
	args = append(args, os.Args[1:]...)
	log.Debugf("re-run as root: command=%s", strings.Join(args, " "))
	cmd := exec.Command("sudo", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		return false, err
	}

	return true, nil
}

func isDockerRootRequired(workspaceInfo *provider2.AgentWorkspaceInfo) bool {
	if workspaceInfo == nil {
		return false
	}
	if workspaceInfo.Agent.Driver != "" && workspaceInfo.Agent.Driver != provider2.DockerDriver {
		return false
	}

	rootRequired, err := dockerReachable(
		workspaceInfo.Agent.Docker.Path,
		workspaceInfo.Agent.Docker.Env,
	)
	if err != nil {
		log.Debugf("error trying to reach docker daemon: error=%v", err)
		return true
	}

	return rootRequired
}

type Exec func(
	ctx context.Context, user string, command string,
	stdin io.Reader, stdout io.Writer, stderr io.Writer,
) error

type TunnelOptions struct {
	Exec    Exec
	User    string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Timeout time.Duration
}

func Tunnel(ctx context.Context, opts TunnelOptions) error {
	if err := InjectAgent(&InjectOptions{
		Ctx: ctx,
		Exec: func(ctx context.Context, command string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
			return opts.Exec(ctx, "root", command, stdin, stdout, stderr)
		},
		IsLocal:                     false,
		RemoteAgentPath:             config.ContainerDevsyHelperLocation,
		DownloadURL:                 config.DefaultAgentDownloadURL(),
		PreferDownloadFromRemoteUrl: Bool(false),
		Timeout:                     opts.Timeout,
	}); err != nil {
		return err
	}

	command := fmt.Sprintf("'%s' internal ssh-server --stdio", config.ContainerDevsyHelperLocation)
	if log.DebugEnabled() {
		command += " --debug"
	}
	user := opts.User
	if user == "" {
		user = "root"
	}

	return opts.Exec(ctx, user, command, opts.Stdin, opts.Stdout, opts.Stderr)
}

func dockerReachable(dockerOverride string, envs map[string]string) (bool, error) {
	docker := "docker"
	if dockerOverride != "" {
		docker = dockerOverride
	}

	if !command.Exists(docker) {
		// if docker is overridden, we assume that there is an error as we don't know how to install the command provided
		if dockerOverride != "" {
			return false, fmt.Errorf("docker command %q not found", dockerOverride)
		}
		// we need root to install docker
		return true, nil
	}

	cmd := exec.Command(docker, "ps")
	if len(envs) > 0 {
		newEnvs := os.Environ()
		for k, v := range envs {
			newEnvs = append(newEnvs, k+"="+v)
		}
		cmd.Env = newEnvs
	}

	_, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(err.Error(), "permission denied") {
			if dockerOverride == "" {
				return true, nil
			}
		}

		return false, fmt.Errorf("%s ps: %w", docker, err)
	}

	return false, nil
}
