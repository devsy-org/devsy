package features

import (
	"encoding/json"
	"fmt"
	"io"
)

const (
	headerFeature = "Feature"
	headerValue   = "Value"
)

func writeJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}
