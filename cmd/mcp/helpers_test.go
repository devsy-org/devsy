package mcp

import (
	"errors"
	"testing"
)

func TestOpResultHandler_Success(t *testing.T) {
	result, ok, err := opResultHandler(func() error { return nil })
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if result != nil {
		t.Fatalf("success path must return nil *CallToolResult, got %+v", result)
	}
	if !ok.OK {
		t.Fatalf("success path must report ok.OK=true")
	}
}

func TestOpResultHandler_Error(t *testing.T) {
	sentinel := errors.New("boom")
	result, ok, err := opResultHandler(func() error { return sentinel })
	if err != nil {
		t.Fatalf(
			"opResultHandler must always return nil go-error so the SDK uses our *CallToolResult, got %v",
			err,
		)
	}
	if result == nil {
		t.Fatalf(
			"error path must return a non-nil *CallToolResult so the SDK reports IsError to the client",
		)
	}
	if !result.IsError {
		t.Fatalf("error path must mark the result IsError=true")
	}
	if ok.OK {
		t.Fatalf("error path must leave ok zero")
	}
}
