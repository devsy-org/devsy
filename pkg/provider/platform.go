package provider

import "github.com/devsy-org/devsy/pkg/types"

type PlatformInstance struct {
	// Provider is the provider name this platform instance belongs to
	Provider string `json:"provider,omitempty"`

	// Host is Devsy Platform host to use
	Host string `json:"host,omitempty"`

	// CreationTimestamp is the timestamp when this platform instance was created
	CreationTimestamp types.Time `json:"creationTimestamp"`
}
