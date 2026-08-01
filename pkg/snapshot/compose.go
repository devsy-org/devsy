package snapshot

import "github.com/devsy-org/devsy/pkg/provider"

// RestoreComposition parses snapshotRef and returns the WorkspaceSource string
// and DevContainerSource override needed to restore a workspace from it:
// "snapshot:<ref>" and "image:<repository>:<tag>-fs". `devsy snapshot restore`
// and `devsy up --from-snapshot` both call this so a snapshot restores
// identically regardless of entry point.
func RestoreComposition(snapshotRef string) (source string, devContainerSource string, err error) {
	ref, err := ParseRef(snapshotRef)
	if err != nil {
		return "", "", err
	}
	return provider.WorkspaceSourceSnapshot + ref.String(), "image:" + ref.FSImageRef(), nil
}
