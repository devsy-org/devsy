package zed

import "strings"

func buildOpenURL(sshHost, workspaceFolder string) string {
	if !strings.HasPrefix(workspaceFolder, "/") {
		workspaceFolder = "/" + workspaceFolder
	}

	return "zed://ssh/" + sshHost + workspaceFolder
}
