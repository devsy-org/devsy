package features

import (
	"encoding/json"
	"fmt"
	"io"
)

const (
	outputJSON = "json"
	outputText = "text"
	outputYAML = "yaml"

	headerFeature = "Feature"
	headerValue   = "Value"
)

func validateOutputFormat(format string) error {
	if format != outputText && format != outputJSON {
		return fmt.Errorf(
			"invalid output format %q: must be %q or %q",
			format,
			outputText,
			outputJSON,
		)
	}
	return nil
}

func writeJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}
