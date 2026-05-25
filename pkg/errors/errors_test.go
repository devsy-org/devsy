package errors

import (
	"encoding/json"
	stderrs "errors"
	"fmt"
	"testing"
)

const (
	testExitErr     = "exit status 1"
	testProviderAWS = "aws"
)

func TestClassify_NilReturnsNil(t *testing.T) {
	if got := Classify(nil, ClassifyContext{}); got != nil {
		t.Fatalf("Classify(nil) = %v, want nil", got)
	}
}

//nolint:funlen // table-driven test; each fingerprint case is a single row.
func TestClassify_Fingerprints(t *testing.T) {
	cases := []struct {
		name    string
		errText string
		stderr  string
		want    Code
	}{
		// Positives — one per fingerprint.
		{
			"aws profile missing",
			"init: exit status 1",
			"failed to get shared config profile, default",
			CodeAWSProfileMissing,
		},
		{
			"aws creds invalid (token)",
			testExitErr,
			"InvalidClientTokenId: The security token included in the request is invalid.",
			CodeAWSCredsInvalid,
		},
		{"aws creds invalid (sig)", testExitErr, "SignatureDoesNotMatch", CodeAWSCredsInvalid},
		{
			"aws region missing",
			testExitErr,
			"MissingRegion: could not find region configuration",
			CodeAWSRegionMissing,
		},
		{"aws region missing alt", testExitErr, "could not find region", CodeAWSRegionMissing},
		{
			"docker not running",
			testExitErr,
			"Cannot connect to the Docker daemon at unix:///var/run/docker.sock",
			CodeDockerNotRunning,
		},
		{
			"docker permission denied",
			testExitErr,
			"permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock",
			CodeDockerPermDenied,
		},
		{
			"kube config missing",
			testExitErr,
			"stat /home/user/.kube/config: no such file or directory",
			CodeKubeConfigMissing,
		},
		{
			"kube unreachable refused",
			testExitErr,
			"dial tcp 127.0.0.1:6443: connection refused",
			CodeKubeUnreachable,
		},
		{
			"podman socket",
			testExitErr,
			"podman.sock: connect: no such file or directory",
			CodePodmanSocket,
		},

		// Negatives — error text that should NOT match these fingerprints.
		{"unrelated text", "something else entirely went wrong", "", CodeUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := fmt.Errorf("%s", tc.errText)
			got := Classify(err, ClassifyContext{Stderr: tc.stderr})
			if got == nil {
				t.Fatalf("Classify returned nil")
			}
			if got.Code != tc.want {
				t.Fatalf("Classify code = %q, want %q (cause=%q)", got.Code, tc.want, got.Cause)
			}
		})
	}
}

func TestClassify_ProviderCatchAll(t *testing.T) {
	err := fmt.Errorf("init: exit status 1")
	got := Classify(err, ClassifyContext{
		Provider: testProviderAWS,
		Stderr:   "totally unrecognised output",
	})
	if got.Code != CodeProviderInitFailed {
		t.Fatalf("Code = %q, want %q", got.Code, CodeProviderInitFailed)
	}
	if got.Provider != testProviderAWS {
		t.Fatalf("Provider = %q, want aws", got.Provider)
	}
}

func TestClassify_UnknownNoProvider(t *testing.T) {
	err := fmt.Errorf("kaboom")
	got := Classify(err, ClassifyContext{})
	if got.Code != CodeUnknown {
		t.Fatalf("Code = %q, want UNKNOWN", got.Code)
	}
	if got.Message != "kaboom" {
		t.Fatalf("Message = %q, want %q", got.Message, "kaboom")
	}
}

func TestClassify_PreservesExistingCLIError(t *testing.T) {
	original := &CLIError{Code: CodeAWSProfileMissing, Message: "x"}
	got := Classify(original, ClassifyContext{Provider: testProviderAWS})
	if got.Code != original.Code || got.Message != original.Message {
		t.Fatalf("Classify lost fields from input CLIError: got %+v", got)
	}
	if got.Provider != testProviderAWS {
		t.Fatalf("Provider not filled; got %q", got.Provider)
	}
}

func TestCLIError_UnwrapPreservesChain(t *testing.T) {
	sentinel := stderrs.New("sentinel")
	wrapped := fmt.Errorf("init: %w", sentinel)
	cliErr := Classify(wrapped, ClassifyContext{})
	if !stderrs.Is(cliErr, sentinel) {
		t.Fatalf("errors.Is should find sentinel through CLIError chain")
	}
}

func TestCLIError_MarshalJSONSnapshot(t *testing.T) {
	e := &CLIError{
		Code:     CodeAWSProfileMissing,
		Message:  "AWS credentials are not configured.",
		Hint:     "Set AWS_PROFILE or create ~/.aws/credentials.",
		DocURL:   "https://example.invalid/aws",
		Provider: testProviderAWS,
		Cause:    "init: exit status 1: failed to get shared config profile, default",
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"code":"AWS_PROFILE_MISSING",` +
		`"message":"AWS credentials are not configured.",` +
		`"hint":"Set AWS_PROFILE or create ~/.aws/credentials.",` +
		`"docUrl":"https://example.invalid/aws",` +
		`"provider":"aws",` +
		`"cause":"init: exit status 1: failed to get shared config profile, default"}`
	if string(b) != want {
		t.Fatalf("JSON mismatch.\n got: %s\nwant: %s", b, want)
	}
}

func TestCLIError_MarshalJSONOmitsEmpties(t *testing.T) {
	e := &CLIError{Code: CodeUnknown, Message: "oops"}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"code":"UNKNOWN","message":"oops"}`
	if string(b) != want {
		t.Fatalf("JSON mismatch.\n got: %s\nwant: %s", b, want)
	}
}

func TestClassify_DoesNotMutateInputCLIError(t *testing.T) {
	original := &CLIError{Code: CodeAWSProfileMissing, Message: "msg"}
	got := Classify(original, ClassifyContext{Provider: "aws", Stderr: "stderr-extra"})
	if got == original {
		t.Fatalf("Classify returned the input pointer; expected a clone")
	}
	if original.Provider != "" {
		t.Fatalf("input Provider was mutated: %q", original.Provider)
	}
	if original.Cause != "" {
		t.Fatalf("input Cause was mutated: %q", original.Cause)
	}
	if got.Provider != "aws" {
		t.Fatalf("clone Provider = %q, want %q", got.Provider, "aws")
	}
}

func TestClassify_NoPanicOnArbitraryInput(t *testing.T) {
	inputs := []string{"", "\x00\x01", "random\nbytes", testExitErr}
	for _, in := range inputs {
		_ = Classify(fmt.Errorf("%s", in), ClassifyContext{Stderr: in})
	}
}
