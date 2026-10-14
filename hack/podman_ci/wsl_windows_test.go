//go:build windows

package main

import (
	"encoding/binary"
	"testing"
)

func TestParseWSLDistributionsUTF16LEAndExactNames(t *testing.T) {
	text := "Ubuntu\r\npodman-machine-default\r\npodman-machine-default-other\r\n"
	units := []uint16{0xfeff}
	for _, r := range text {
		units = append(units, uint16(r))
	}
	data := make([]byte, 2+len(units)*2)
	data[0], data[1] = 0xff, 0xfe
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[2+i*2:], unit)
	}
	names := parseWSLDistributions(data)
	if !containsExact(names, "podman-machine-default") {
		t.Fatalf("distribution not decoded: %#v", names)
	}
	if containsExact(names, "podman-machine-default-other") == false {
		t.Fatalf("suffix distribution was lost: %#v", names)
	}
}

func TestMachineAbsentRequiresExactCleanError(t *testing.T) {
	failed := func(output string) CommandResult { code := 1; return CommandResult{ExitCode: &code, Stderr: output} }
	if !isDefaultMachineAbsent(failed("Error: podman-machine-default: VM does not exist\n")) {
		t.Fatal("exact machine absence not recognized")
	}
	for _, result := range []CommandResult{
		{TimedOut: true, Stderr: "podman-machine-default: VM does not exist"},
		failed("another-machine: VM does not exist"),
		failed("podman-machine-default: VM does not exist\npipe busy"),
	} {
		if isDefaultMachineAbsent(result) {
			t.Fatalf("accepted non-exact absence: %+v", result)
		}
	}
}
