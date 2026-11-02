package workspacelogs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
)

// SpoolDirName is the private subdirectory of a container data directory that
// holds the container-side spool.
const SpoolDirName = "workspace-logs"

// Availability reasons shared by the readers on both sides of a refresh.
const (
	ReasonRefreshUnsupported = "refresh_unsupported"
	ReasonSourceOffline      = "source_offline"
	ReasonRefreshFailed      = "refresh_failed"
	ReasonNoSpool            = "no_spool"

	// unavailableMarker prefixes the stderr line that carries a typed reason.
	unavailableMarker = "devsy-setup-logs-unavailable: "
	// maxMarkerBytes bounds the reason text taken from a remote stderr.
	maxMarkerBytes = 256
)

// FormatUnavailable returns the stderr line a reader prints before exiting
// non-zero, so the caller can tell unavailable history from a command error.
func FormatUnavailable(reason string) string {
	return unavailableMarker + reason + "\n"
}

// ParseUnavailable extracts a typed reason from remote stderr. It returns
// false when stderr carries no marker.
func ParseUnavailable(stderr string) (string, bool) {
	_, rest, found := strings.Cut(stderr, unavailableMarker)
	if !found {
		return "", false
	}
	reason, _, _ := strings.Cut(rest, "\n")
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > maxMarkerBytes {
		return "", false
	}

	return reason, true
}

// ResolveSpool returns the root of an existing container spool. It checks the
// primary and the non-root fallback data directories in that order and never
// creates, chmods or probes anything. ErrNotFound means no spool exists.
func ResolveSpool() (string, error) {
	return resolveSpoolIn([]string{config.ContainerDataDir, config.ContainerDataDirFallback})
}

func resolveSpoolIn(dataDirs []string) (string, error) {
	var unsafeErr error
	for _, dir := range dataDirs {
		root, err := validateSpoolRoot(dir)
		switch {
		case err == nil:
			return root, nil
		case errors.Is(err, ErrNotFound):
		default:
			unsafeErr = errors.Join(unsafeErr, err)
		}
	}
	if unsafeErr != nil {
		return "", unsafeErr
	}

	return "", ErrNotFound
}

// validateSpoolRoot checks dataDir and its spool subdirectory. Only the final
// components are inspected: the parent is a fixed system directory such as
// /tmp, which some systems expose as a symlink.
func validateSpoolRoot(dataDir string) (string, error) {
	if err := checkOwnedDir(dataDir); err != nil {
		return "", err
	}
	root := filepath.Join(dataDir, SpoolDirName)
	if err := checkOwnedDir(root); err != nil {
		return "", err
	}

	return root, nil
}

// checkOwnedDir accepts a real directory owned by the current user that is not
// writable by group or others.
func checkOwnedDir(dir string) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsafePath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrUnsafePath, dir)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%w: %s is writable by others", ErrUnsafePath, dir)
	}
	if !ownedByCurrentUser(info) {
		return fmt.Errorf("%w: %s has another owner", ErrUnsafePath, dir)
	}

	return nil
}

// openExisting returns a Store over an existing root without creating or
// pruning anything.
func openExisting(root string) (*Store, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("%w: root must be absolute", ErrUnsafePath)
	}

	return &Store{root: root, now: time.Now, tails: map[string]tail{}}, nil
}

// SpoolSelector selects one page of a container-side read.
type SpoolSelector struct {
	// Root is the validated spool root from ResolveSpool.
	Root string
	// Context and WorkspaceUID must match the stored run. An empty UID skips
	// the UID check only when the run is not bound to one.
	Context      string
	WorkspaceUID string
	Read         ReadRequest
}

// ReadSpoolPage returns one bounded page of a spooled run. It validates the
// selectors again, so a caller cannot read a run of another context or
// workspace generation.
func ReadSpoolPage(sel SpoolSelector) (*ReadResponse, error) {
	if err := requireID("runId", sel.Read.RunID); err != nil {
		return nil, err
	}
	s, err := openExisting(sel.Root)
	if err != nil {
		return nil, err
	}
	m, err := s.Manifest(sel.Read.RunID)
	if err != nil {
		return nil, err
	}
	if err = checkRunIdentity(m, sel.Context, sel.WorkspaceUID); err != nil {
		return nil, err
	}

	return s.Read(sel.Read)
}

// checkRunIdentity rejects a run that belongs to another context or workspace
// generation. A run bound to a UID needs the caller to name the same UID.
func checkRunIdentity(m *RunManifest, contextName, uid string) error {
	if m.Context != contextName {
		return fmt.Errorf("%w: run belongs to another context", ErrIdentityConflict)
	}
	if m.WorkspaceUID != nil && *m.WorkspaceUID != uid {
		return fmt.Errorf("%w: run belongs to another workspace", ErrIdentityConflict)
	}

	return nil
}

// DecodeReadResponse decodes one remote page, refusing input larger than
// MaxPageBytes.
func DecodeReadResponse(r io.Reader) (*ReadResponse, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxPageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(data) > MaxPageBytes {
		return nil, fmt.Errorf("%w: response exceeds %d bytes", ErrInvalidRecord, MaxPageBytes)
	}

	return decodeReadResponse(data)
}
