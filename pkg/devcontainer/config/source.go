package config

import "encoding/json"

// ConfigSource records explicit values before typed decoding loses field presence.
// It is never serialized as part of a devcontainer configuration.
type ConfigSource struct {
	Origin string
	Fields map[string]json.RawMessage
}

func (c *DevContainerConfig) captureSource(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	c.Sources = []ConfigSource{{Origin: c.Origin, Fields: fields}}
	return nil
}
