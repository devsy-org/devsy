package marimo

import (
	"fmt"
	"os/exec"
	"strconv"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/ide"
	"github.com/devsy-org/devsy/pkg/log"
)

const (
	BindAddressOption = "BIND_ADDRESS"
)

const DefaultServerPort = 10800

var Options = ide.Options{
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
	if err := o.installMarimo(); err != nil {
		return err
	}
	return o.Start()
}

func (o *MarimoServer) Start() error {
	return command.StartBackgroundOnce("marimo", func() (*exec.Cmd, error) {
		log.Infof("Starting marimo in background...")
		runCommand := fmt.Sprintf(
			"marimo edit --headless --host 0.0.0.0 --port %s --no-token '%s'",
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

func (o *MarimoServer) installMarimo() error {
	if command.ExistsForUser("marimo", o.userName) {
		return nil
	}

	baseCommand := pickPip(o.userName)
	if baseCommand == "" {
		return fmt.Errorf(
			"seems like neither pip3 nor pip exists, please make sure to install python correctly",
		)
	}

	runCommand := fmt.Sprintf("%s install marimo", baseCommand)
	args := []string{}
	if o.userName != "" {
		args = append(args, "su", o.userName, "-c", runCommand)
	} else {
		args = append(args, "sh", "-c", runCommand)
	}

	log.Infof("installing marimo")
	//nolint:gosec // args are constructed from trusted inputs
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"error installing marimo: %w",
			command.WrapCommandError(out, err),
		)
	}

	log.Info("installed marimo")
	return nil
}

func pickPip(userName string) string {
	switch {
	case command.ExistsForUser("pip3", userName):
		return "pip3"
	case command.ExistsForUser("pip", userName):
		return "pip"
	default:
		return ""
	}
}
