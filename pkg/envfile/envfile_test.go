package envfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const (
	testEnvVal = "value"
	testEnvNew = "new"
)

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

	MergeAndApply(map[string]string{"DEVSY_ENVFILE_TEST_NEW": testEnvVal})

	data, err := os.ReadFile(path) //#nosec G304
	if err != nil {
		t.Fatalf("read envfile: %v", err)
	}

	var ef EnvFile
	if err := json.Unmarshal(data, &ef); err != nil {
		t.Fatalf("parse envfile: %v", err)
	}

	if got := ef.Env["DEVSY_ENVFILE_TEST_NEW"]; got != testEnvVal {
		t.Fatalf("envfile DEVSY_ENVFILE_TEST_NEW = %q, want %q", got, testEnvVal)
	}

	if got := os.Getenv("DEVSY_ENVFILE_TEST_NEW"); got != testEnvVal {
		t.Fatalf("env DEVSY_ENVFILE_TEST_NEW = %q, want %q", got, testEnvVal)
	}
}

func TestMergeAndApplyMergesWithExistingFile(t *testing.T) {
	path := useTempLocation(t)
	t.Setenv("DEVSY_ENVFILE_TEST_KEEP", "")
	t.Setenv("DEVSY_ENVFILE_TEST_ADD", "")

	writeFile(t, path, `{"env":{"DEVSY_ENVFILE_TEST_KEEP":"old"}}`)

	MergeAndApply(map[string]string{"DEVSY_ENVFILE_TEST_ADD": testEnvNew})

	data, err := os.ReadFile(path) // #nosec G304
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
	if got := ef.Env["DEVSY_ENVFILE_TEST_ADD"]; got != testEnvNew {
		t.Fatalf("new key missing: got %q, want %q", got, testEnvNew)
	}

	if got := os.Getenv("DEVSY_ENVFILE_TEST_KEEP"); got != "old" {
		t.Fatalf("env DEVSY_ENVFILE_TEST_KEEP = %q, want %q", got, "old")
	}
	if got := os.Getenv("DEVSY_ENVFILE_TEST_ADD"); got != testEnvNew {
		t.Fatalf("env DEVSY_ENVFILE_TEST_ADD = %q, want %q", got, testEnvNew)
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
