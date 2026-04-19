package server

import (
	"os"

	"github.com/devsy-org/devsy/pkg/command"
)

func findWorkdir(workdir string, userName string) string {
	// check if requested workdir exists
	if workdir != "" {
		_, err := os.Stat(workdir)
		if err == nil {
			return workdir
		}
	}

	// fall back to home directory
	home, _ := command.GetHome(userName)
	if _, err := os.Stat(home); err == nil {
		workdir = home
	}

	return workdir
}
