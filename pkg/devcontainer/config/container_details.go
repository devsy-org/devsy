package config

import (
	"encoding/json"
	"strings"
)

type ImageDetails struct {
	ID     string
	Config ImageDetailsConfig
}

type ImageDetailsConfig struct {
	User       string
	Env        []string
	Labels     map[string]string
	Entrypoint []string
	Cmd        []string
}

type ContainerDetails struct {
	ID      string                 `json:"ID,omitempty"`
	Created string                 `json:"Created,omitempty"`
	State   ContainerDetailsState  `json:"State"`
	Config  ContainerDetailsConfig `json:"Config"`
	Mounts  []ContainerMount       `json:"Mounts,omitempty"`
}

// ContainerMount represents a mount point on a container as returned by
// docker/podman inspect.
type ContainerMount struct {
	Type        string `json:"Type,omitempty"`
	Source      string `json:"Source,omitempty"`
	Destination string `json:"Destination,omitempty"`
}

type ContainerDetailsConfig struct {
	Labels map[string]string `json:"Labels,omitempty"`

	// WorkingDir specifies default working directory inside the container
	WorkingDir string `json:"WorkingDir,omitempty"`

	// User specifies the user that the container runs as
	User string `json:"User,omitempty"`

	// LegacyImage shouldn't get used anymore and is only there for testing
	LegacyImage string `json:"Image,omitempty"`
}

type ContainerDetailsState struct {
	Status    ContainerStatus `json:"Status,omitempty"`
	StartedAt string          `json:"StartedAt,omitempty"`
	ExitCode  int             `json:"ExitCode,omitempty"`
	Error     string          `json:"Error,omitempty"`
}

// UnmarshalJSON decodes inspect output and normalizes Status, so the field is
// always canonical regardless of the runtime's casing.
func (s *ContainerDetailsState) UnmarshalJSON(data []byte) error {
	type alias ContainerDetailsState
	var raw alias
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	raw.Status = ToContainerStatus(string(raw.Status))
	*s = ContainerDetailsState(raw)
	return nil
}

// ContainerStatus is a normalized container state string (`State.Status` from
// `docker inspect` and friends), comparable against the ContainerStatus*
// constants.
type ContainerStatus string

const (
	ContainerStatusRunning    ContainerStatus = "running"
	ContainerStatusExited     ContainerStatus = "exited"
	ContainerStatusCreated    ContainerStatus = "created"
	ContainerStatusPaused     ContainerStatus = "paused"
	ContainerStatusRestarting ContainerStatus = "restarting"
	ContainerStatusDead       ContainerStatus = "dead"
	ContainerStatusRemoving   ContainerStatus = "removing"
)

// ToContainerStatus normalizes a raw status string for comparison.
func ToContainerStatus(s string) ContainerStatus {
	return ContainerStatus(strings.ToLower(s))
}
