//go:build linux

package main

import (
	"strings"
	"testing"
)

func TestContainersConfigUsesConfiguredCrunAndNewline(t *testing.T) {
	got := containersConfig("/custom/crun")
	want := "[engine]\nruntime = \"crun\"\n\n[engine.runtimes]\ncrun = [\"/custom/crun\"]\n"
	if got != want {
		t.Fatalf("containersConfig() = %q", got)
	}
}

func TestPatchAppArmorIsExactAndIdempotent(t *testing.T) {
	input := []byte("profile podman /usr/bin/podman flags=(attach_disconnected) {\n}")
	got, changed := patchAppArmor(input)
	if !changed || !strings.Contains(string(got), "/usr/{bin,local/bin}/podman") {
		t.Fatalf("patch failed: %q", got)
	}
	gotAgain, changedAgain := patchAppArmor(got)
	if changedAgain || string(gotAgain) != string(got) {
		t.Fatal("patch was not idempotent")
	}
	unrelated, changed := patchAppArmor([]byte("profile other /usr/bin/podman {\n}"))
	if changed || string(unrelated) != "profile other /usr/bin/podman {\n}" {
		t.Fatal("unrelated profile changed")
	}
}
