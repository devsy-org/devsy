package secrets_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/secrets"
)

func TestParseSecretsFile(t *testing.T) {
	content := `# Database credentials
DB_HOST=localhost
DB_PASSWORD=s3cr3t

# API key
API_KEY=abc123
`
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

func TestParseSecretsFile_CommentsAndBlankLines(t *testing.T) {
	content := `
# This is a comment

KEY=value
  # indented comment

OTHER=val
`
	path := writeTemp(t, content)

	got, err := secrets.ParseSecretsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got["KEY"] != "value" {
		t.Errorf("KEY = %q, want %q", got["KEY"], "value")
	}
	if got["OTHER"] != "val" {
		t.Errorf("OTHER = %q, want %q", got["OTHER"], "val")
	}
}

func TestParseSecretsFile_ValueWithEquals(t *testing.T) {
	content := `CONNECTION_STRING=host=db port=5432 user=admin password=p=ss
TOKEN=abc==def
`
	path := writeTemp(t, content)

	got, err := secrets.ParseSecretsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got["CONNECTION_STRING"] != "host=db port=5432 user=admin password=p=ss" {
		t.Errorf("CONNECTION_STRING = %q", got["CONNECTION_STRING"])
	}
	if got["TOKEN"] != "abc==def" {
		t.Errorf("TOKEN = %q, want %q", got["TOKEN"], "abc==def")
	}
}

func TestParseSecretsFile_MissingFile(t *testing.T) {
	_, err := secrets.ParseSecretsFile("/nonexistent/path/secrets.env")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestParseSecretsFile_EmptyFile(t *testing.T) {
	path := writeTemp(t, "")

	got, err := secrets.ParseSecretsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(got))
	}
}

func TestParseSecretsFile_WhitespaceAroundKey(t *testing.T) {
	content := "  MY_KEY  =some_value\n"
	path := writeTemp(t, content)

	got, err := secrets.ParseSecretsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got["MY_KEY"] != "some_value" {
		t.Errorf("MY_KEY = %q, want %q", got["MY_KEY"], "some_value")
	}
}

func TestParseSecretsFile_MissingSeparator(t *testing.T) {
	content := "INVALID_LINE\n"
	path := writeTemp(t, content)

	_, err := secrets.ParseSecretsFile(path)
	if err == nil {
		t.Fatal("expected error for line without = separator")
	}
}

func TestParseSecretsFile_EmptyValue(t *testing.T) {
	content := "EMPTY_VAL=\n"
	path := writeTemp(t, content)

	got, err := secrets.ParseSecretsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got["EMPTY_VAL"] != "" {
		t.Errorf("EMPTY_VAL = %q, want empty string", got["EMPTY_VAL"])
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secrets.env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
