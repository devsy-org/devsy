package apple

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

const (
	stateRunning  = "running"
	stateExited   = "exited"
	mountTypeBind = "bind"
	archUnknown   = "unknown" // placeholder arch in Apple's multi-arch image index
)

// Apple's inspect JSON is unrelated to Docker's; these structs mirror the
// relevant subset and map onto the Docker-shaped config types, keeping all
// schema coupling in this file.
type containerInspect struct {
	ID            string             `json:"id"`
	Configuration containerConfig    `json:"configuration"`
	Status        containerStatusRaw `json:"status"`
}

type containerConfig struct {
	ID           string            `json:"id"`
	CreationDate string            `json:"creationDate"`
	Image        containerImageRef `json:"image"`
	InitProcess  initProcess       `json:"initProcess"`
	Labels       map[string]string `json:"labels"`
	Mounts       []containerMount  `json:"mounts"`
	Platform     platform          `json:"platform"`
}

type containerImageRef struct {
	Reference string `json:"reference"`
}

type initProcess struct {
	WorkingDirectory string  `json:"workingDirectory"`
	User             userRef `json:"user"`
}

type userRef struct {
	ID userID `json:"id"`
}

type userID struct {
	UID int `json:"uid"`
	GID int `json:"gid"`
}

type containerMount struct {
	Type        mountType `json:"type"`
	Source      string    `json:"source"`
	Destination string    `json:"destination"`
}

// mountType decodes Apple's mount type, an object keyed by the type name
// (e.g. {"virtiofs": {}}) rather than a plain string.
type mountType string

func (m *mountType) UnmarshalJSON(data []byte) error {
	// Tolerate a plain string form as well as the object form.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*m = mountType(s)
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	for k := range obj {
		*m = mountType(k)
		return nil
	}
	return nil
}

// dockerType maps a virtiofs host share onto the Docker "bind" vocabulary.
func (m mountType) dockerType() string {
	if m == "virtiofs" {
		return mountTypeBind
	}
	return string(m)
}

type platform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
}

type containerStatusRaw struct {
	State       string `json:"state"`
	StartedDate string `json:"startedDate"`
}

func (c containerInspect) toContainerDetails() config.ContainerDetails {
	id := c.ID
	if id == "" {
		id = c.Configuration.ID
	}

	mounts := make([]config.ContainerMount, 0, len(c.Configuration.Mounts))
	for _, m := range c.Configuration.Mounts {
		mounts = append(mounts, config.ContainerMount{
			Type:        m.Type.dockerType(),
			Source:      m.Source,
			Destination: m.Destination,
		})
	}

	return config.ContainerDetails{
		ID:      id,
		Created: c.Configuration.CreationDate,
		State: config.ContainerDetailsState{
			Status:    normalizeState(c.Status.State),
			StartedAt: c.Status.StartedDate,
		},
		Config: config.ContainerDetailsConfig{
			Labels:      c.Configuration.Labels,
			WorkingDir:  c.Configuration.InitProcess.WorkingDirectory,
			User:        fmt.Sprintf("%d", c.Configuration.InitProcess.User.ID.UID),
			LegacyImage: c.Configuration.Image.Reference,
		},
		Mounts: mounts,
	}
}

func normalizeState(state string) string {
	s := strings.ToLower(strings.TrimSpace(state))
	if s == "stopped" {
		// Docker uses "exited" for a container that ran and stopped; the
		// runner's terminal-state checks key off that vocabulary.
		return stateExited
	}
	return s
}

type imageInspect struct {
	ID            string           `json:"id"`
	Configuration imageConfigOuter `json:"configuration"`
	Variants      []imageVariant   `json:"variants"`
}

type imageConfigOuter struct {
	Name string `json:"name"`
}

type imageVariant struct {
	Config imageVariantConfig `json:"config"`
}

type imageVariantConfig struct {
	Architecture string      `json:"architecture"`
	OS           string      `json:"os"`
	Config       ociImageCfg `json:"config"`
}

type ociImageCfg struct {
	User       string            `json:"User"`
	Env        []string          `json:"Env"`
	Cmd        []string          `json:"Cmd"`
	Entrypoint []string          `json:"Entrypoint"`
	Labels     map[string]string `json:"Labels"`
	WorkingDir string            `json:"WorkingDir"`
}

// toImageDetails prefers the variant matching preferArch, else the first known-arch variant.
func (i imageInspect) toImageDetails(preferArch string) *config.ImageDetails {
	variant := i.selectVariant(preferArch)
	if variant == nil {
		return &config.ImageDetails{ID: i.ID}
	}

	return &config.ImageDetails{
		ID: i.ID,
		Config: config.ImageDetailsConfig{
			User:       variant.Config.Config.User,
			Env:        variant.Config.Config.Env,
			Labels:     variant.Config.Config.Labels,
			Entrypoint: variant.Config.Config.Entrypoint,
			Cmd:        variant.Config.Config.Cmd,
		},
	}
}

func (i imageInspect) selectVariant(preferArch string) *imageVariant {
	var fallback *imageVariant
	for idx := range i.Variants {
		v := &i.Variants[idx]
		arch := strings.ToLower(v.Config.Architecture)
		if arch == "" || arch == archUnknown {
			continue
		}
		if preferArch != "" && arch == strings.ToLower(preferArch) {
			return v
		}
		if fallback == nil {
			fallback = v
		}
	}
	return fallback
}
