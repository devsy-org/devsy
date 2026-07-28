package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/devsy-org/devsy/pkg/types"
)

// objectKeyOrder returns the keys of the named top-level object field in the
// order they appear in the JSON document. It returns nil when the field is
// absent or is not a JSON object.
func objectKeyOrder(data []byte, field string) ([]string, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	value, ok := raw[field]
	if !ok {
		return nil, nil
	}

	dec := json.NewDecoder(bytes.NewReader(value))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, nil
	}

	var keys []string
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected %s object key token: %v", field, keyTok)
		}
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

type FeatureSet struct {
	ConfigID string
	Version  string
	Folder   string
	Config   *FeatureConfig
	Options  any
}

type FeatureConfig struct {
	// ID of the Feature. The id should be unique in the context of the repository/published package where the feature exists and must match the name of the directory where the devcontainer-feature.json resides.
	ID string `json:"id,omitempty"`

	// Display name of the Feature.
	Name string `json:"name,omitempty"`

	// The version of the Feature. Follows the semanatic versioning (semver) specification.
	Version string `json:"version,omitempty"`

	// Description of the Feature. For the best appearance in an implementing tool, refrain from including markdown or HTML in the description.
	Description string `json:"description,omitempty"`

	// Entrypoint script that should fire at container start up.
	Entrypoint string `json:"entrypoint,omitempty"`

	// Indicates that the Feature is deprecated, and will not receive any further updates/support. This property is intended to be used by the supporting tools for highlighting Feature deprecation.
	Deprecated bool `json:"deprecated,omitempty"`

	// Array of old IDs used to publish this Feature. The property is useful for renaming a currently published Feature within a single namespace.
	LegacyIds []string `json:"legacyIds,omitempty"`

	// Possible user-configurable options for this Feature. The selected options will be passed as environment variables when installing the Feature into the container.
	Options map[string]FeatureConfigOption `json:"options,omitempty"`

	// URL to documentation for the Feature.
	DocumentationURL string `json:"documentationURL,omitempty"`

	// URL to the license for the Feature.
	LicenseURL string `json:"licenseURL,omitempty"`

	// Passes docker capabilities to include when creating the dev container.
	CapAdd []string `json:"capAdd,omitempty"`

	// Adds the tiny init process to the container (--init) when the Feature is used.
	Init *bool `json:"init,omitempty"`

	// Sets privileged mode (--privileged) for the container.
	Privileged *bool `json:"privileged,omitempty"`

	// Sets container security options to include when creating the container.
	SecurityOpt []string `json:"securityOpt,omitempty"`

	// Mounts a volume or bind mount into the container.
	Mounts []*Mount `json:"mounts,omitempty"`

	// Array of ID's of Features that should execute before this one. Allows control for feature authors on soft dependencies between different Features.
	InstallsAfter []string `json:"installsAfter,omitempty"`

	// The optional dependsOn property indicates a set of required, “hard” dependencies for a given Feature. Hard dependencies must be satisfied before this Feature is installed.
	DependsOn DependsOnField `json:"dependsOn,omitempty"`

	// Container environment variables.
	ContainerEnv map[string]string `json:"containerEnv,omitempty"`

	// Lifecycle hooks
	DevContainerActions `json:",inline"`

	// OCI manifest annotations (populated only for OCI-sourced features).
	Annotations map[string]string `json:"-"`

	// Origin is the path where the feature was loaded from
	Origin string `json:"-"`

	// dependsOnOrder preserves the declaration order of DependsOn keys as they
	// appear in devcontainer-feature.json. The reference devcontainer CLI emits
	// lockfile dependsOn arrays in this order (Object.keys), so we retain it to
	// produce byte-identical lockfiles.
	dependsOnOrder []string `json:"-"`
}

// UnmarshalJSON decodes a FeatureConfig while capturing the declaration order
// of the dependsOn object's keys, which a plain map cannot preserve.
func (c *FeatureConfig) UnmarshalJSON(data []byte) error {
	type alias FeatureConfig
	if err := json.Unmarshal(data, (*alias)(c)); err != nil {
		return err
	}
	order, err := objectKeyOrder(data, "dependsOn")
	if err != nil {
		return err
	}
	c.dependsOnOrder = order
	return nil
}

// DependsOnKeys returns the dependency feature identifiers in their original
// declaration order, matching the reference devcontainer CLI's lockfile output.
// It falls back to sorted keys when order was not captured (e.g. a
// programmatically constructed config).
func (c *FeatureConfig) DependsOnKeys() []string {
	if len(c.dependsOnOrder) == len(c.DependsOn) {
		return c.dependsOnOrder
	}
	keys := make([]string, 0, len(c.DependsOn))
	for k := range c.DependsOn {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type DependsOnField map[string]any

func (d *DependsOnField) UnmarshalJSON(data []byte) error {
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		var arr []string
		if json.Unmarshal(data, &arr) == nil {
			return fmt.Errorf("dependsOn must be an object mapping feature IDs to options")
		}
		return err
	}
	*d = DependsOnField(obj)
	return nil
}

type FeatureConfigOption struct {
	// Default value if the user omits this option from their configuration.
	Default types.StrBool `json:"default,omitempty"`

	// A description of the option displayed to the user by a supporting tool.
	Description string `json:"description,omitempty"`

	// The type of the option. Can be 'boolean' or 'string'.  Options of type 'string' should use the 'enum' or 'proposals' property to provide a list of allowed values.
	Type string `json:"type,omitempty"`

	// Allowed values for this option.  Unlike 'proposals', the user cannot provide a custom value not included in the 'enum' array.
	Enum []string `json:"enum,omitempty"`

	// Suggested values for this option.  Unlike 'enum', the 'proposals' attribute indicates the installation script can handle arbitrary values provided by the user.
	Proposals []string `json:"proposals,omitempty"`
}
