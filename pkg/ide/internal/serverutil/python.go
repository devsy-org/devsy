package serverutil

import (
	"fmt"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/ide"
	"github.com/devsy-org/devsy/pkg/log"
)

type PythonTool struct {
	Name       string
	Package    string
	Executable string
}

// InstallPythonTool installs tool for userName unless its executable already exists.
func InstallPythonTool(userName string, tool PythonTool) error {
	if command.ExistsForUser(tool.Executable, userName) {
		return nil
	}

	runCommand, err := ide.PythonInstallCommand(userName, tool.Package)
	if err != nil {
		return err
	}

	log.Infof("installing %s", tool.Name)
	out, err := SuOrSh(userName, runCommand, "").CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"error installing %s: %w",
			tool.Name,
			command.WrapCommandError(out, err),
		)
	}

	log.Info("installed " + tool.Name)
	return nil
}
