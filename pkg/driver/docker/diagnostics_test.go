package docker

import (
	"reflect"
	"testing"
)

func TestExtractBindSources(t *testing.T) {
	args := []string{
		"run", "--sig-proxy=false",
		"--mount", "type=bind,source=/a/b,target=/x,consistency=consistent",
		"--mount", "type=bind,src=/c/d,dst=/y",
		"--mount=type=bind,src=/e/f,dst=/z",
		"--mount", "type=volume,source=myvol,target=/v", // not a bind, ignored
		"alpine",
	}
	got := extractBindSources(args)
	want := []string{"/a/b", "/c/d", "/e/f"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractBindSources() = %v, want %v", got, want)
	}
}
