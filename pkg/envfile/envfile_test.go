package envfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// useTempLocation points the package-level envfile location at a path inside a
// temp directory so tests never touch the real /etc/envfile.json. It returns the
// full path and a cleanup func that also unsets any leaked environment variables.
func useTempLocation(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "envfile.json")

	original := location
	location = path
	t.Cleanup(func() {
		location = original
	})

	return path
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestApplyNoFileIsNoOp(t *testing.T) {
	useTempLocation(t)

	// Ensure the var is not already set in the environment.
	if _, ok := os.LookupEnv("DEVSY_ENVFILE_TEST_ABSENT"); ok {
		t.Fatal("precondition: DEVSY_ENVFILE_TEST_ABSENT already set")
	}

	Apply()

	if _, ok := os.LookupEnv("DEVSY_ENVFILE_TEST_ABSENT"); ok {
		t.Fatal("Apply set env from missing file")
	}
}

func TestApplySetsEnvFromValidFile(t *testing.T) {
	path := useTempLocation(t)
	t.Setenv("DEVSY_ENVFILE_TEST_FOO", "")
	writeFile(t, path, `{"env":{"DEVSY_ENVFILE_TEST_FOO":"bar"}}`)

	Apply()

	if got := os.Getenv("DEVSY_ENVFILE_TEST_FOO"); got != "bar" {
		t.Fatalf("DEVSY_ENVFILE_TEST_FOO = %q, want %q", got, "bar")
	}
}

func TestApplyMalformedJSONIsNoOp(t *testing.T) {
	path := useTempLocation(t)
	t.Setenv("DEVSY_ENVFILE_TEST_FOO", "keep")
	writeFile(t, path, `{not-json`)

	Apply()

	// Existing value must be preserved; malformed file must not clear it.
	if got := os.Getenv("DEVSY_ENVFILE_TEST_FOO"); got != "keep" {
		t.Fatalf("DEVSY_ENVFILE_TEST_FOO = %q, want %q", got, "keep")
	}
}

func TestApplyEmptyEnvMapIsNoOp(t *testing.T) {
	path := useTempLocation(t)
	if _, ok := os.LookupEnv("DEVSY_ENVFILE_TEST_EMPTY"); ok {
		t.Fatal("precondition: DEVSY_ENVFILE_TEST_EMPTY already set")
	}
	writeFile(t, path, `{"env":{}}`)

	Apply()

	if _, ok := os.LookupEnv("DEVSY_ENVFILE_TEST_EMPTY"); ok {
		t.Fatal("Apply set env from empty env map")
	}
}

func TestMergeAndApplyEmptyInputDoesNotWrite(t *testing.T) {
	path := useTempLocation(t)

	MergeAndApply(nil)
	MergeAndApply(map[string]string{})

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("env file written for empty input, err=%v", err)
	}
}

func TestMergeAndApplyCreatesFileWhenAbsent(t *testing.T) {
	path := useTempLocation(t)
	t.Setenv("DEVSY_ENVFILE_TEST_NEW", "")

	MergeAndApply(map[string]string{"DEVSY_ENVFILE_TEST_NEW": "value"})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read envfile: %v", err)
	}

	var ef EnvFile
	if err := json.Unmarshal(data, &ef); err != nil {
		t.Fatalf("parse envfile: %v", err)
	}

	if got := ef.Env["DEVSY_ENVFILE_TEST_NEW"]; got != "value" {
		t.Fatalf("envfile DEVSY_ENVFILE_TEST_NEW = %q, want %q", got, "value")
	}

	if got := os.Getenv("DEVSY_ENVFILE_TEST_NEW"); got != "value" {
		t.Fatalf("env DEVSY_ENVFILE_TEST_NEW = %q, want %q", got, "value")
	}
}

func TestMergeAndApplyMergesWithExistingFile(t *testing.T) {
	path := useTempLocation(t)
	t.Setenv("DEVSY_ENVFILE_TEST_KEEP", "")
	t.Setenv("DEVSY_ENVFILE_TEST_ADD", "")

	writeFile(t, path, `{"env":{"DEVSY_ENVFILE_TEST_KEEP":"old"}}`)

	MergeAndApply(map[string]string{"DEVSY_ENVFILE_TEST_ADD": "new"})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read envfile: %v", err)
	}

	var ef EnvFile
	if err := json.Unmarshal(data, &ef); err != nil {
		t.Fatalf("parse envfile: %v", err)
	}

	if got := ef.Env["DEVSY_ENVFILE_TEST_KEEP"]; got != "old" {
		t.Fatalf("existing key overwritten: got %q, want %q", got, "old")
	}
	if got := ef.Env["DEVSY_ENVFILE_TEST_ADD"]; got != "new" {
		t.Fatalf("new key missing: got %q, want %q", got, "new")
	}

	if got := os.Getenv("DEVSY_ENVFILE_TEST_KEEP"); got != "old" {
		t.Fatalf("env DEVSY_ENVFILE_TEST_KEEP = %q, want %q", got, "old")
	}
	if got := os.Getenv("DEVSY_ENVFILE_TEST_ADD"); got != "new" {
		t.Fatalf("env DEVSY_ENVFILE_TEST_ADD = %q, want %q", got, "new")
	}
}

func TestMergeAndApplyMalformedFileIsNoOp(t *testing.T) {
	path := useTempLocation(t)
	t.Setenv("DEVSY_ENVFILE_TEST_MERGE", "keep")
	writeFile(t, path, `{not-json`)

	MergeAndApply(map[string]string{"DEVSY_ENVFILE_TEST_MERGE": "should-not-apply"})

	if got := os.Getenv("DEVSY_ENVFILE_TEST_MERGE"); got != "keep" {
		t.Fatalf("DEVSY_ENVFILE_TEST_MERGE = %q, want %q", got, "keep")
	}
}
