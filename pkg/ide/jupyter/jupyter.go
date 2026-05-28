package jupyter

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

const DefaultServerPort = 10700

var Options = ide.Options{
	BindAddressOption: {
		Name:        BindAddressOption,
		Description: "The address to bind the server to locally, e.g. 0.0.0.0:12345",
		Default:     "",
	},
}

type JupyterNotbookServer struct {
	values          map[string]config.OptionValue
	workspaceFolder string
	userName        string
}

func NewJupyterNotebookServer(
	workspaceFolder string,
	userName string,
	values map[string]config.OptionValue,
) *JupyterNotbookServer {
	return &JupyterNotbookServer{
		values:          values,
		workspaceFolder: workspaceFolder,
		userName:        userName,
	}
}

func (o *JupyterNotbookServer) Install() error {
	if err := o.installNotebook(); err != nil {
		return err
	}
	return o.Start()
}

func (o *JupyterNotbookServer) Start() error {
	return command.StartBackgroundOnce("jupyter", func() (*exec.Cmd, error) {
		log.Infof("Starting jupyter notebook in background...")
		runCommand := fmt.Sprintf(
			"jupyter notebook --ip='*' --NotebookApp.notebook_dir='%s' --NotebookApp.token='' "+
				"--NotebookApp.password='' --no-browser --port '%s' --allow-root",
			o.workspaceFolder,
			strconv.Itoa(DefaultServerPort),
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

func (o *JupyterNotbookServer) installNotebook() error {
	if command.ExistsForUser("jupyter", o.userName) {
		return nil
	}

	runCommand, err := ide.PythonInstallCommand(o.userName, "notebook")
	if err != nil {
		return err
	}

	args := []string{}
	if o.userName != "" {
		args = append(args, "su", o.userName, "-c", runCommand)
	} else {
		args = append(args, "sh", "-c", runCommand)
	}

	log.Infof("installing jupyter notebook")
	//nolint:gosec // args are constructed from trusted inputs
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"error installing jupyter notebook: %w",
			command.WrapCommandError(out, err),
		)
	}

	log.Info("installed jupyter notebook")
	return nil
}
