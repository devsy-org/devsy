package config

import "encoding/json"

// convert round-trips a value through JSON to reshape it into another type,
// e.g. from a map[string]any into a concrete struct.
func convert(from, to any) error {
	out, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(out, to)
}
