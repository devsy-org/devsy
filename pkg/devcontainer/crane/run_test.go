package crane

import (
	"os"
	"strings"
	"testing"
)

func TestCommandRunUsesBoundedRedactedDiagnostics(t *testing.T) {
	script := t.TempDir() + "/crane"
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'output=%s\\n' \"$2\"\nprintf 'stderr=%s\\n' \"$2\" >&2\nexit 7\n"), 0o700); err != nil {
		t.Fatalf("write fake crane: %v", err)
	}
	t.Setenv(envDevsyCraneName, script)
	const secret = "DEVSY_CRANE_SECRET_846297"
	craneSigningKey = secret
	t.Cleanup(func() { craneSigningKey = "" })

	_, err := New(DecryptCommand).WithArg(secret).Run()
	if err == nil {
		t.Fatal("Run succeeded, want failure")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("secret escaped crane error: %v", err)
	}
	if !strings.Contains(err.Error(), "stderr=***") {
		t.Fatalf("redacted stderr missing from crane error: %v", err)
	}
}
