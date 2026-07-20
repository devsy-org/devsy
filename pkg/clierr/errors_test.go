package clierr

import (
	"encoding/json"
	stderrs "errors"
	"fmt"
	"testing"

	"go.uber.org/zap/zapcore"
)

const testRateLimited = "rate limited"

func TestClassify_NilReturnsNil(t *testing.T) {
	if got := Classify(nil); got != nil {
		t.Fatalf("Classify(nil) = %v, want nil", got)
	}
}

func TestClassify_Unknown(t *testing.T) {
	got := Classify(fmt.Errorf("kaboom"))
	if got.Code != CodeUnknown {
		t.Fatalf("Code = %q, want UNKNOWN", got.Code)
	}
	if got.Message != "kaboom" {
		t.Fatalf("Message = %q, want %q", got.Message, "kaboom")
	}
}

func TestClassify_RateLimited(t *testing.T) {
	got := Classify(fmt.Errorf("wrapped: %w", ErrRateLimited))
	if got.Code != CodeRateLimited {
		t.Fatalf("Code = %q, want %q", got.Code, CodeRateLimited)
	}
	if got.Message == "" {
		t.Fatal("rate-limited error produced empty message")
	}
}

func TestClassify_PreservesExistingCLIError(t *testing.T) {
	original := &CLIError{Code: CodeRateLimited, Message: "x"}
	got := Classify(original)
	if got.Code != original.Code || got.Message != original.Message {
		t.Fatalf("Classify lost fields from input CLIError: got %+v", got)
	}
}

func TestClassify_FindsWrappedCLIError(t *testing.T) {
	inner := &CLIError{Code: CodeRateLimited, Message: testRateLimited}
	wrapped := fmt.Errorf("unable to update: %w", fmt.Errorf("detect: %w", inner))
	got := Classify(wrapped)
	if got.Code != CodeRateLimited {
		t.Fatalf("Code = %q, want %q", got.Code, CodeRateLimited)
	}
	if got.Message != testRateLimited {
		t.Fatalf("Message = %q, want %q", got.Message, testRateLimited)
	}
}

func TestCLIError_UnwrapPreservesChain(t *testing.T) {
	sentinel := stderrs.New("sentinel")
	wrapped := fmt.Errorf("init: %w", sentinel)
	cliErr := Classify(wrapped)
	if !stderrs.Is(cliErr, sentinel) {
		t.Fatalf("errors.Is should find sentinel through CLIError chain")
	}
}

func TestNewPanic(t *testing.T) {
	got := NewPanic("boom")
	if got.Code != CodePanic {
		t.Fatalf("Code = %q, want %q", got.Code, CodePanic)
	}
	if got.Message == "" {
		t.Fatal("panic message should not be empty")
	}
}

func TestCLIError_MarshalJSONSnapshot(t *testing.T) {
	e := &CLIError{Code: CodeRateLimited, Message: testRateLimited}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"code":"RATE_LIMITED","message":"rate limited"}`
	if string(b) != want {
		t.Fatalf("JSON mismatch.\n got: %s\nwant: %s", b, want)
	}
}

func TestCLIError_LogObjectShape(t *testing.T) {
	enc := zapcore.NewMapObjectEncoder()
	e := &CLIError{Code: CodeRateLimited, Message: testRateLimited}
	if err := e.MarshalLogObject(enc); err != nil {
		t.Fatalf("MarshalLogObject: %v", err)
	}
	if len(enc.Fields) != 2 {
		t.Fatalf("wire object must be exactly {code, message}, got %v", enc.Fields)
	}
	if enc.Fields["code"] != string(CodeRateLimited) {
		t.Fatalf("code = %v, want %q", enc.Fields["code"], CodeRateLimited)
	}
	if enc.Fields["message"] != testRateLimited {
		t.Fatalf("message = %v", enc.Fields["message"])
	}
}
