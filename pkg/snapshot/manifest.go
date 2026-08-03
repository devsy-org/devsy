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
	// defaultContainerImageMediaType is used only when
	// BuildManifestOptions.ContainerImageMediaType is unset. Real callers
	// (cmd/snapshot/create.go) always pass the media type the registry
	// actually reported for the pushed image, which may be the OCI or the
	// Docker v2 manifest format depending on the daemon/registry.
	defaultContainerImageMediaType = "application/vnd.docker.distribution.manifest.v2+json"
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
	// AnnotationRunArgs is the create-time devcontainer.json's runArgs
	// (JSON-encoded []string), replayed onto the restored container so
	// runArgs the original devcontainer.json relied on (e.g.
	// --add-host=host.docker.internal:host-gateway for a registry reachable
	// only via that hostname) still apply — restore pins DevContainerSource
	// to the committed image, which bypasses the project devcontainer.json
	// and would otherwise silently drop them.
	AnnotationRunArgs = "sh.devsy.snapshot.run-args"
	// AnnotationContainerEnv is the create-time devcontainer.json's
	// containerEnv (JSON-encoded map[string]string), replayed onto the
	// restored container for the same reason as AnnotationRunArgs: restore
	// pins DevContainerSource to the committed image, bypassing the project
	// devcontainer.json and silently dropping any containerEnv it set.
	AnnotationContainerEnv = "sh.devsy.snapshot.container-env"
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
	RunArgs          []string
	ContainerEnv     map[string]string

	ContainerImageMediaType string
	ContainerImageDigest    string
	ContainerImageSize      int64

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
	if err := addDevContainerOverrideAnnotations(annotations, opts); err != nil {
		return nil, err
	}

	imageMediaType := opts.ContainerImageMediaType
	if imageMediaType == "" {
		imageMediaType = defaultContainerImageMediaType
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
				MediaType: imageMediaType,
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

// addDevContainerOverrideAnnotations JSON-encodes the create-time
// devcontainer.json settings that restore must replay onto the image-sourced
// synthesized config (see pkg/devcontainer's rawConfigFromSource), each only
// when non-empty so an unset override doesn't leave a spurious annotation.
func addDevContainerOverrideAnnotations(
	annotations map[string]string, opts BuildManifestOptions,
) error {
	if len(opts.RunArgs) > 0 {
		raw, err := json.Marshal(opts.RunArgs)
		if err != nil {
			return fmt.Errorf("build snapshot manifest: marshal run args: %w", err)
		}
		annotations[AnnotationRunArgs] = string(raw)
	}
	if len(opts.ContainerEnv) > 0 {
		raw, err := json.Marshal(opts.ContainerEnv)
		if err != nil {
			return fmt.Errorf("build snapshot manifest: marshal container env: %w", err)
		}
		annotations[AnnotationContainerEnv] = string(raw)
	}
	return nil
}

// RunArgs decodes the create-time devcontainer.json's runArgs from the
// manifest, or returns nil when the snapshot carries none.
func (m *Manifest) RunArgs() ([]string, error) {
	raw, ok := m.Annotations[AnnotationRunArgs]
	if !ok || raw == "" {
		return nil, nil
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, fmt.Errorf("parse %s annotation: %w", AnnotationRunArgs, err)
	}
	return args, nil
}

// ContainerEnv decodes the create-time devcontainer.json's containerEnv from
// the manifest, or returns nil when the snapshot carries none.
func (m *Manifest) ContainerEnv() (map[string]string, error) {
	raw, ok := m.Annotations[AnnotationContainerEnv]
	if !ok || raw == "" {
		return nil, nil
	}
	var env map[string]string
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil, fmt.Errorf("parse %s annotation: %w", AnnotationContainerEnv, err)
	}
	return env, nil
}

// ContainerImage returns the manifest's committed container filesystem
// layer, or an error if the manifest's layer contract isn't satisfied (see
// validateLayers).
func (m *Manifest) ContainerImage() (Descriptor, error) {
	imgIdx, _, err := validateLayers(m.Layers)
	if err != nil {
		return Descriptor{}, err
	}
	return m.Layers[imgIdx], nil
}

// Volumes returns the manifest's volumes archive layer, or an error if the
// manifest's layer contract isn't satisfied (see validateLayers).
func (m *Manifest) Volumes() (Descriptor, error) {
	_, volIdx, err := validateLayers(m.Layers)
	if err != nil {
		return Descriptor{}, err
	}
	return m.Layers[volIdx], nil
}

// validateLayers enforces the snapshot manifest layer contract: exactly one
// container image layer (any media type other than VolumesMediaType) and
// exactly one VolumesMediaType layer, no duplicates of either, and no other
// layers. Returns their indices into layers.
func validateLayers(layers []Descriptor) (imgIdx, volIdx int, err error) {
	imgIdx, volIdx = -1, -1
	for i, l := range layers {
		if l.MediaType == VolumesMediaType {
			if volIdx != -1 {
				return -1, -1, fmt.Errorf("snapshot manifest has more than one volumes layer")
			}
			volIdx = i
			continue
		}
		if imgIdx != -1 {
			return -1, -1, fmt.Errorf("snapshot manifest has more than one container image layer")
		}
		imgIdx = i
	}
	if imgIdx == -1 {
		return -1, -1, fmt.Errorf("snapshot manifest has no container image layer")
	}
	if volIdx == -1 {
		return -1, -1, fmt.Errorf("snapshot manifest has no volumes layer")
	}
	return imgIdx, volIdx, nil
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
	if m.MediaType != ManifestMediaType {
		return nil, fmt.Errorf(
			"parse snapshot manifest: unsupported mediaType %q, expected %q",
			m.MediaType,
			ManifestMediaType,
		)
	}
	if _, _, err := validateLayers(m.Layers); err != nil {
		return nil, fmt.Errorf("parse snapshot manifest: %w", err)
	}
	return m, nil
}

var (
	emptyConfigBytes  = []byte("{}")
	emptyConfigDigest = "sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"
)
