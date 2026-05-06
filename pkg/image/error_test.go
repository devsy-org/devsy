package image

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

func makeTransportErrorWithBody(statusCode int, body string) error {
	req, _ := http.NewRequest("GET", "https://ghcr.io/v2/devsy-org/test-images/", nil)
	resp := &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
	return transport.CheckError(resp, http.StatusOK)
}

func TestSanitizeRegistryError_HTMLBody(t *testing.T) {
	htmlBody := `<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN"><html><body><h1>403 Forbidden</h1></body></html>`
	inner := makeTransportErrorWithBody(http.StatusForbidden, htmlBody)
	wrapped := fmt.Errorf("retrieve image ghcr.io/devsy-org/test-images/base:ubuntu: %w", inner)

	sanitized := SanitizeRegistryError(wrapped)
	if sanitized == wrapped {
		t.Fatal("expected error to be sanitized, got original back")
	}
	want := "unexpected status code 403 Forbidden"
	if sanitized.Error() != want {
		t.Errorf("got %q, want %q", sanitized.Error(), want)
	}
}

func TestSanitizeRegistryError_HTMLWithSmallTag(t *testing.T) {
	htmlBody := `<html><head><title>Error</title></head><body>Service Unavailable</body></html>`
	inner := makeTransportErrorWithBody(http.StatusServiceUnavailable, htmlBody)

	sanitized := SanitizeRegistryError(inner)
	want := "unexpected status code 503 Service Unavailable"
	if sanitized.Error() != want {
		t.Errorf("got %q, want %q", sanitized.Error(), want)
	}
}

func TestSanitizeRegistryError_JSONBodyPassesThrough(t *testing.T) {
	jsonBody := `{"errors":[{"code":"DENIED","message":"access denied"}]}`
	inner := makeTransportErrorWithBody(http.StatusForbidden, jsonBody)
	wrapped := fmt.Errorf("retrieve image example.com/foo:latest: %w", inner)

	sanitized := SanitizeRegistryError(wrapped)
	if sanitized != wrapped {
		t.Error("expected JSON-body error to pass through unchanged")
	}
}

func TestSanitizeRegistryError_PlainTextBodyPassesThrough(t *testing.T) {
	inner := makeTransportErrorWithBody(http.StatusInternalServerError, "internal server error")
	wrapped := fmt.Errorf("retrieve image example.com/foo:latest: %w", inner)

	sanitized := SanitizeRegistryError(wrapped)
	if sanitized != wrapped {
		t.Error("expected plain text error to pass through unchanged")
	}
}

func TestSanitizeRegistryError_NonTransportError(t *testing.T) {
	err := errors.New("network timeout")
	sanitized := SanitizeRegistryError(err)
	if sanitized != err {
		t.Error("expected non-transport error to pass through unchanged")
	}
}

func TestSanitizeRegistryError_Nil(t *testing.T) {
	sanitized := SanitizeRegistryError(nil)
	if sanitized != nil {
		t.Error("expected nil to pass through as nil")
	}
}

func TestSanitizeRegistryError_EmptyBodyPassesThrough(t *testing.T) {
	inner := makeTransportErrorWithBody(http.StatusForbidden, "")
	sanitized := SanitizeRegistryError(inner)
	if sanitized != inner {
		t.Error("expected empty-body error to pass through unchanged")
	}
}
