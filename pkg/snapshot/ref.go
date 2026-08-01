package snapshot

import (
	"fmt"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/random"
)

const (
	refTimeLayout = "20060102150405"
	// refSuffixLength is the size of the random suffix appended after the
	// timestamp. Two snapshots for the same workspace created in the same
	// UTC second would otherwise collide on an identical tag, silently
	// clobbering the earlier snapshot's manifest on push; the random suffix
	// makes that collision astronomically unlikely instead of guaranteed.
	refSuffixLength = 6
)

// Ref identifies a snapshot as <repository>:<workspace-id>-<timestamp>-<random>.
type Ref struct {
	Repository  string
	WorkspaceID string
	Timestamp   time.Time
	Tag         string
}

func NewRef(repository, workspaceID string, at time.Time) (*Ref, error) {
	if repository == "" {
		return nil, fmt.Errorf("build snapshot ref: registry/repository is required")
	}
	if workspaceID == "" {
		return nil, fmt.Errorf("build snapshot ref: workspace id is required")
	}
	tag := workspaceID + "-" + at.UTC().Format(refTimeLayout) + "-" + random.String(refSuffixLength)
	full := repository + ":" + tag
	if _, err := parseTag(full); err != nil {
		return nil, fmt.Errorf("build snapshot ref %q: %w", full, err)
	}
	return &Ref{
		Repository:  repository,
		WorkspaceID: workspaceID,
		Timestamp:   at.UTC(),
		Tag:         tag,
	}, nil
}

func ParseRef(s string) (*Ref, error) {
	tagRef, err := parseTag(s)
	if err != nil {
		return nil, fmt.Errorf("parse snapshot ref %q: %w", s, err)
	}

	tag := tagRef.TagStr()
	workspaceID, tsPart, err := splitWorkspaceIDAndTimestamp(tag)
	if err != nil {
		return nil, fmt.Errorf("parse snapshot ref %q: %w", s, err)
	}

	ts, err := time.Parse(refTimeLayout, tsPart)
	if err != nil {
		return nil, fmt.Errorf("parse snapshot ref %q: invalid timestamp %q: %w", s, tsPart, err)
	}

	return &Ref{
		Repository:  tagRef.Context().Name(),
		WorkspaceID: workspaceID,
		Timestamp:   ts,
		Tag:         tag,
	}, nil
}

// splitWorkspaceIDAndTimestamp peels the trailing "-<random>" suffix off tag,
// then splits the remainder on the last hyphen into workspaceID and the
// timestamp portion. Older refs pushed before refSuffixLength existed have
// no random suffix and fail to parse here; none are expected to still be
// referenced once this ships.
func splitWorkspaceIDAndTimestamp(tag string) (workspaceID, timestamp string, err error) {
	suffixIdx := strings.LastIndex(tag, "-")
	if suffixIdx <= 0 || suffixIdx == len(tag)-1 {
		return "", "", fmt.Errorf("tag %q is not <workspace-id>-<timestamp>-<random>", tag)
	}
	withoutSuffix := tag[:suffixIdx]

	idx := strings.LastIndex(withoutSuffix, "-")
	if idx <= 0 || idx == len(withoutSuffix)-1 {
		return "", "", fmt.Errorf("tag %q is not <workspace-id>-<timestamp>-<random>", tag)
	}
	return withoutSuffix[:idx], withoutSuffix[idx+1:], nil
}

func (r *Ref) String() string {
	return r.Repository + ":" + r.Tag
}

// FSImageRef is the single place that owns the "-fs" suffix convention:
// `snapshot create` pushes the committed container image under this ref, and
// `snapshot restore` / `up --from-snapshot` point DevContainerSource at it.
func (r *Ref) FSImageRef() string {
	return r.Repository + ":" + r.Tag + "-fs"
}
