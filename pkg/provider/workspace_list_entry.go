package provider

type WorkspaceListEntry struct {
	*Workspace

	DisplayName          string `json:"displayName"`
	SSHHostAlias         string `json:"sshHostAlias"`
	EffectiveDisplayName string `json:"effectiveDisplayName"`
	PreferredSSHHost     string `json:"preferredSSHHost"`
	CanonicalSSHHost     string `json:"canonicalSSHHost"`
}

func NewWorkspaceListEntry(ws *Workspace) *WorkspaceListEntry {
	return &WorkspaceListEntry{
		Workspace:            ws,
		DisplayName:          ws.DisplayName,
		SSHHostAlias:         ws.SSHHostAlias,
		EffectiveDisplayName: ws.EffectiveDisplayName(),
		PreferredSSHHost:     ws.PreferredSSHHost(),
		CanonicalSSHHost:     ws.CanonicalSSHHost(),
	}
}

func NewWorkspaceListEntries(workspaces []*Workspace) []*WorkspaceListEntry {
	if workspaces == nil {
		return nil
	}

	entries := make([]*WorkspaceListEntry, 0, len(workspaces))
	for _, ws := range workspaces {
		entries = append(entries, NewWorkspaceListEntry(ws))
	}

	return entries
}
