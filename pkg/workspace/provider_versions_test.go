package workspace

import (
	"errors"
	"testing"
)

func TestErrVersionListUnsupported(t *testing.T) {
	if ErrVersionListUnsupported == nil {
		t.Fatal("ErrVersionListUnsupported must be defined")
	}
	wrapped := errors.New("wrapped: " + ErrVersionListUnsupported.Error())
	if !errors.Is(errors.Join(ErrVersionListUnsupported, wrapped), ErrVersionListUnsupported) {
		t.Fatal("errors.Is must work against ErrVersionListUnsupported")
	}
}

func TestProviderVersionFields(t *testing.T) {
	v := ProviderVersion{Tag: "v1.0.0", Prerelease: false, Current: true}
	if v.Tag != "v1.0.0" || !v.Current {
		t.Fatal("fields must round-trip")
	}
}
