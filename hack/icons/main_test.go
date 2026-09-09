package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func dummyPNG() []byte {
	return []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01, 0x02, 0x03}
}

func parseICNSTags(data []byte) []string {
	offset := 8
	var found []string
	for offset < len(data) {
		tag := string(data[offset : offset+4])
		chunkLen := binary.BigEndian.Uint32(data[offset+4 : offset+8])
		found = append(found, tag)
		offset += int(chunkLen)
	}
	return found
}

func TestPackICNS(t *testing.T) {
	frames := make(map[int][]byte)
	for _, entry := range icnsTags {
		frames[entry.size] = dummyPNG()
	}

	data, err := packICNS(frames)
	if err != nil {
		t.Fatalf("packICNS failed: %v", err)
	}

	if len(data) < 8 || string(data[:4]) != "icns" {
		t.Fatalf("invalid icns header")
	}

	totalLen := binary.BigEndian.Uint32(data[4:8])
	if int(totalLen) != len(data) {
		t.Errorf("expected length %d, got %d", len(data), totalLen)
	}

	foundTags := parseICNSTags(data)
	expectedTags := []string{
		"icp4", "icp5", "icp6", "ic07", "ic08", "ic09",
		"ic10", "ic11", "ic12", "ic13", "ic14",
	}
	for _, expected := range expectedTags {
		if !slices.Contains(foundTags, expected) {
			t.Errorf("missing expected ICNS tag: %s", expected)
		}
	}
}

func checkICOEntry(t *testing.T, data []byte, idx, size int) {
	entryOffset := 6 + idx*16
	w := int(data[entryOffset])
	if size == 256 && w != 0 {
		t.Errorf("entry %d: expected 0 for width 256, got %d", idx, w)
	} else if size != 256 && w != size {
		t.Errorf("entry %d: expected width %d, got %d", idx, size, w)
	}

	imgSize := binary.LittleEndian.Uint32(data[entryOffset+8 : entryOffset+12])
	imgOffset := binary.LittleEndian.Uint32(data[entryOffset+12 : entryOffset+16])

	imgData := data[imgOffset : imgOffset+imgSize]
	if !bytes.HasPrefix(imgData, []byte{0x89, 0x50, 0x4e, 0x47}) {
		t.Errorf("entry %d: image data at %d is not PNG", idx, imgOffset)
	}
}

func TestPackICO(t *testing.T) {
	frames := make(map[int][]byte)
	for _, s := range icoSizes {
		frames[s] = dummyPNG()
	}

	data, err := packICO(frames)
	if err != nil {
		t.Fatalf("packICO failed: %v", err)
	}

	if len(data) < 6 {
		t.Fatalf("ico too short: %d", len(data))
	}

	reserved := binary.LittleEndian.Uint16(data[0:2])
	icoType := binary.LittleEndian.Uint16(data[2:4])
	count := binary.LittleEndian.Uint16(data[4:6])

	if reserved != 0 || icoType != 1 || int(count) != len(icoSizes) {
		t.Errorf("invalid ico header: reserved=%d, type=%d, count=%d", reserved, icoType, count)
	}

	for i, s := range icoSizes {
		checkICOEntry(t, data, i, s)
	}
}

func TestValidateSVG(t *testing.T) {
	tmpDir := t.TempDir()

	validSVG := filepath.Join(tmpDir, "valid.svg")
	validContent := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024"></svg>`)
	if err := os.WriteFile(validSVG, validContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateSVG(validSVG); err != nil {
		t.Errorf("expected valid SVG to pass, got: %v", err)
	}

	invalidSVG := filepath.Join(tmpDir, "invalid.svg")
	if err := os.WriteFile(
		invalidSVG,
		[]byte(`<html><body>not svg</body></html>`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := validateSVG(invalidSVG); err == nil {
		t.Error("expected invalid SVG to fail")
	}
}
