package secrets_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/secrets"
)

func TestParseSecretsFile(t *testing.T) {
	content := `{
  "DB_HOST": "localhost",
  "DB_PASSWORD": "s3cr3t",
  "API_KEY": "abc123"
}`
	path := writeTemp(t, content)

	got, err := secrets.ParseSecretsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	if got["DB_HOST"] != "localhost" {
		t.Errorf("DB_HOST = %q, want %q", got["DB_HOST"], "localhost")
	}
	if got["DB_PASSWORD"] != "s3cr3t" {
		t.Errorf("DB_PASSWORD = %q, want %q", got["DB_PASSWORD"], "s3cr3t")
	}
	if got["API_KEY"] != "abc123" {
		t.Errorf("API_KEY = %q, want %q", got["API_KEY"], "abc123")
	}
}

func TestParseSecretsFile_ValueWithSpecialChars(t *testing.T) {
	content := `{"CONNECTION_STRING": "host=db port=5432 password=p=ss", "MULTILINE": "line1\nline2"}`
	path := writeTemp(t, content)

	got, err := secrets.ParseSecretsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got["CONNECTION_STRING"] != "host=db port=5432 password=p=ss" {
		t.Errorf("CONNECTION_STRING = %q", got["CONNECTION_STRING"])
	}
	if got["MULTILINE"] != "line1\nline2" {
		t.Errorf("MULTILINE = %q, want two lines", got["MULTILINE"])
	}
}

func TestParseSecretsFile_EmptyObject(t *testing.T) {
	path := writeTemp(t, "{}")

	got, err := secrets.ParseSecretsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(got))
	}
}

func TestParseSecretsFile_MissingFile(t *testing.T) {
	_, err := secrets.ParseSecretsFile("/nonexistent/path/secrets.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestParseSecretsFile_InvalidJSON(t *testing.T) {
	path := writeTemp(t, "{not valid json")

	_, err := secrets.ParseSecretsFile(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secrets.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
