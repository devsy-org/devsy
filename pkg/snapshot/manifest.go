package snapshot

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	VolumesMediaType     = "application/vnd.devsy.snapshot.volumes.v1.tar+gzip"
	ManifestMediaType    = "application/vnd.oci.image.manifest.v1+json"
	ManifestArtifactType = "application/vnd.devsy.snapshot.manifest.v1+json"
	emptyConfigMediaType = "application/vnd.oci.empty.v1+json"
	dockerImageMediaType = "application/vnd.docker.distribution.manifest.v2+json"
)

const (
	AnnotationWorkspaceUID     = "sh.devsy.snapshot.workspace-uid"
	AnnotationCreatedAt        = "sh.devsy.snapshot.created-at"
	AnnotationParent           = "sh.devsy.snapshot.parent"
	AnnotationDevContainerHash = "sh.devsy.snapshot.devcontainer-hash"
	AnnotationSourceProvider   = "sh.devsy.snapshot.source-provider"
	AnnotationMessage          = "sh.devsy.snapshot.message"
	// AnnotationMountPrefix is the create-time mount target path (leading "/"
	// trimmed) that volumes archive entries are prefixed with. Restore must
	// strip exactly this many path segments regardless of the restore-side
	// mount target's own depth, since the two are not guaranteed to match
	// (different provider defaults, different workspace-folder conventions).
	AnnotationMountPrefix = "sh.devsy.snapshot.mount-prefix"
)

// Descriptor mirrors the OCI content descriptor fields we need; kept minimal
// rather than depending on go-containerregistry's v1.Descriptor so this
// package stays free of registry I/O concerns.
type Descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

type Manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	ArtifactType  string            `json:"artifactType,omitempty"`
	Config        Descriptor        `json:"config"`
	Layers        []Descriptor      `json:"layers"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

type BuildManifestOptions struct {
	WorkspaceUID     string
	CreatedAt        time.Time
	Parent           string
	DevContainerHash string
	SourceProvider   string
	Message          string
	MountPrefix      string

	ContainerImageDigest string
	ContainerImageSize   int64

	VolumesDigest string
	VolumesSize   int64
}

// BuildManifest builds a snapshot manifest referencing the committed
// container image and the volumes blob by digest, annotated per the
// sh.devsy.snapshot.* convention.
func BuildManifest(opts BuildManifestOptions) (*Manifest, error) {
	if opts.WorkspaceUID == "" {
		return nil, fmt.Errorf("build snapshot manifest: workspace uid is required")
	}
	if opts.ContainerImageDigest == "" {
		return nil, fmt.Errorf("build snapshot manifest: container image digest is required")
	}
	if opts.VolumesDigest == "" {
		return nil, fmt.Errorf("build snapshot manifest: volumes digest is required")
	}

	annotations := map[string]string{
		AnnotationWorkspaceUID:     opts.WorkspaceUID,
		AnnotationCreatedAt:        opts.CreatedAt.UTC().Format(time.RFC3339),
		AnnotationParent:           opts.Parent,
		AnnotationDevContainerHash: opts.DevContainerHash,
		AnnotationSourceProvider:   opts.SourceProvider,
		AnnotationMountPrefix:      opts.MountPrefix,
	}
	if opts.Message != "" {
		annotations[AnnotationMessage] = opts.Message
	}

	return &Manifest{
		SchemaVersion: 2,
		MediaType:     ManifestMediaType,
		ArtifactType:  ManifestArtifactType,
		Config: Descriptor{
			MediaType: emptyConfigMediaType,
			Digest:    emptyConfigDigest,
			Size:      int64(len(emptyConfigBytes)),
		},
		Layers: []Descriptor{
			{
				MediaType: dockerImageMediaType,
				Digest:    opts.ContainerImageDigest,
				Size:      opts.ContainerImageSize,
			},
			{
				MediaType: VolumesMediaType,
				Digest:    opts.VolumesDigest,
				Size:      opts.VolumesSize,
			},
		},
		Annotations: annotations,
	}, nil
}

func (m *Manifest) MarshalOCI() ([]byte, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot manifest: %w", err)
	}
	return raw, nil
}

func ParseManifest(raw []byte) (*Manifest, error) {
	m := &Manifest{}
	if err := json.Unmarshal(raw, m); err != nil {
		return nil, fmt.Errorf("parse snapshot manifest: %w", err)
	}
	if m.SchemaVersion != 2 {
		return nil, fmt.Errorf(
			"parse snapshot manifest: unsupported schemaVersion %d, expected 2",
			m.SchemaVersion,
		)
	}
	if m.ArtifactType != ManifestArtifactType {
		return nil, fmt.Errorf(
			"parse snapshot manifest: unsupported artifactType %q, expected %q (not a snapshot manifest?)",
			m.ArtifactType,
			ManifestArtifactType,
		)
	}
	return m, nil
}

var (
	emptyConfigBytes  = []byte("{}")
	emptyConfigDigest = "sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"
)
