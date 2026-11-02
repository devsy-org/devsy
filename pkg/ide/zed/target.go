package zed

// Target names the workspace Zed connects to over SSH.
type Target struct {
	UserName        string
	WorkspaceFolder string
	WorkspaceID     string
}
