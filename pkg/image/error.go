package image

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

var htmlMarkers = []string{"<!DOCTYPE", "<html", "<HTML", "<head", "<HEAD"}

func containsHTML(s string) bool {
	for _, marker := range htmlMarkers {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

func SanitizeRegistryError(err error) error {
	if err == nil {
		return nil
	}

	var terr *transport.Error
	if !errors.As(err, &terr) {
		return err
	}

	if !containsHTML(err.Error()) {
		return err
	}

	return fmt.Errorf(
		"unexpected status code %d %s",
		terr.StatusCode,
		http.StatusText(terr.StatusCode),
	)
}
