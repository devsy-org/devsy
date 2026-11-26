package marimo

import (
	"fmt"
	"os/exec"
	"strconv"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/ide"
	"github.com/devsy-org/devsy/pkg/ide/internal/serverutil"
	"github.com/devsy-org/devsy/pkg/log"
)

const (
	BindAddressOption  = "BIND_ADDRESS"
	ForwardPortsOption = "FORWARD_PORTS"
)

const DefaultServerPort = 10800

const marimoTool = "marimo"

var Options = ide.Options{
	ForwardPortsOption: {
		Name:        ForwardPortsOption,
		Description: "If Devsy should automatically do port-forwarding",
		Default:     "true",
		Enum:        []string{"true", "false"},
	},
	BindAddressOption: {
		Name:        BindAddressOption,
		Description: "The address to bind the server to locally, e.g. 0.0.0.0:12345",
		Default:     "",
	},
}

type MarimoServer struct {
	values          map[string]config.OptionValue
	workspaceFolder string
	userName        string
}

func NewMarimoServer(
	workspaceFolder string,
	userName string,
	values map[string]config.OptionValue,
) *MarimoServer {
	return &MarimoServer{
		values:          values,
		workspaceFolder: workspaceFolder,
		userName:        userName,
	}
}

func (o *MarimoServer) Install() error {
	if err := serverutil.InstallPythonTool(o.userName, serverutil.PythonTool{
		Name:       marimoTool,
		Package:    marimoTool,
		Executable: marimoTool,
	}); err != nil {
		return err
	}
	return o.Start()
}

func (o *MarimoServer) Start() error {
	return command.StartBackgroundOnce("marimo", func() (*exec.Cmd, error) {
		log.Infof("Starting marimo in background")
		runCommand := fmt.Sprintf(
			"marimo edit --headless --host 0.0.0.0 --port %s --no-token %q",
			strconv.Itoa(DefaultServerPort),
			o.workspaceFolder,
		)
		args := []string{}
		if o.userName != "" {
			args = append(args, "su", o.userName, "-w", "SSH_AUTH_SOCK", "-l", "-c", runCommand)
		} else {
			args = append(args, "sh", "-l", "-c", runCommand)
		}
		//nolint:gosec // args are constructed from trusted inputs
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = o.workspaceFolder
		return cmd, nil
	})
}
