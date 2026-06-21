package main

import (
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

const (
	OSDarwin  = "darwin"
	OSLinux   = "linux"
	OSWindows = "windows"

	ArchAMD64 = "amd64"
	ArchARM64 = "arm64"
)

type Arch struct {
	GOOS   string
	GOARCH string
}

func (a Arch) String() string { return a.GOOS + "/" + a.GOARCH }

func FromFile(path string) (Arch, error) {
	f, err := os.Open(path) // #nosec G304 -- caller-controlled release tooling.
	if err != nil {
		return Arch{}, err
	}
	defer func() { _ = f.Close() }()

	var head [4]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return Arch{}, fmt.Errorf("read header: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Arch{}, err
	}

	switch {
	case isMachO(head):
		return machoArch(f)
	case isPE(head):
		return peArch(f)
	case isELF(head):
		return elfArch(f)
	}
	return Arch{}, fmt.Errorf("unrecognized binary header: %x", head)
}

func isMachO(h [4]byte) bool {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		switch order.Uint32(h[:]) {
		case macho.Magic32, macho.Magic64, macho.MagicFat:
			return true
		}
	}
	return false
}

func isPE(h [4]byte) bool { return h[0] == 'M' && h[1] == 'Z' }

func isELF(h [4]byte) bool {
	return h[0] == 0x7f && h[1] == 'E' && h[2] == 'L' && h[3] == 'F'
}

func machoArch(f *os.File) (Arch, error) {
	mf, err := macho.NewFile(f)
	if err != nil {
		return Arch{}, fmt.Errorf("parse mach-o: %w", err)
	}
	defer func() { _ = mf.Close() }()
	switch mf.Cpu {
	case macho.CpuAmd64:
		return Arch{GOOS: OSDarwin, GOARCH: ArchAMD64}, nil
	case macho.CpuArm64:
		return Arch{GOOS: OSDarwin, GOARCH: ArchARM64}, nil
	}
	return Arch{}, fmt.Errorf("unsupported mach-o cpu: %v", mf.Cpu)
}

func elfArch(f *os.File) (Arch, error) {
	ef, err := elf.NewFile(f)
	if err != nil {
		return Arch{}, fmt.Errorf("parse elf: %w", err)
	}
	defer func() { _ = ef.Close() }()
	switch ef.Machine {
	case elf.EM_X86_64:
		return Arch{GOOS: OSLinux, GOARCH: ArchAMD64}, nil
	case elf.EM_AARCH64:
		return Arch{GOOS: OSLinux, GOARCH: ArchARM64}, nil
	}
	return Arch{}, fmt.Errorf("unsupported elf machine: %v", ef.Machine)
}

func peArch(f *os.File) (Arch, error) {
	machine, err := readPEMachine(f)
	if err != nil {
		return Arch{}, err
	}
	switch machine {
	case peMachineAMD64:
		return Arch{GOOS: OSWindows, GOARCH: ArchAMD64}, nil
	case peMachineARM64:
		return Arch{GOOS: OSWindows, GOARCH: ArchARM64}, nil
	}
	return Arch{}, fmt.Errorf("unsupported pe machine: 0x%x", machine)
}

const (
	peMachineAMD64 uint16 = 0x8664
	peMachineARM64 uint16 = 0xaa64
)

func readPEMachine(f *os.File) (uint16, error) {
	if _, err := f.Seek(0x3c, io.SeekStart); err != nil {
		return 0, err
	}
	var peOffset uint32
	if err := binary.Read(f, binary.LittleEndian, &peOffset); err != nil {
		return 0, fmt.Errorf("read pe offset: %w", err)
	}
	if _, err := f.Seek(int64(peOffset), io.SeekStart); err != nil {
		return 0, err
	}
	var sig [4]byte
	if _, err := io.ReadFull(f, sig[:]); err != nil {
		return 0, fmt.Errorf("read pe signature: %w", err)
	}
	if sig != [4]byte{'P', 'E', 0, 0} {
		return 0, fmt.Errorf("not a pe file: signature %x", sig)
	}
	var machine uint16
	if err := binary.Read(f, binary.LittleEndian, &machine); err != nil {
		return 0, fmt.Errorf("read pe machine: %w", err)
	}
	return machine, nil
}
