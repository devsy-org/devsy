//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
	"slices"
	"strings"
	"unicode/utf16"
)

func parseWSLDistributions(output []byte) []string {
	text := normalizeWSLOutput(output)
	var names []string
	start := 0
	for i := 0; i <= len(text); i++ {
		if i == len(text) || text[i] == '\n' {
			line := strings.TrimSpace(text[start:i])
			if line != "" {
				names = append(names, line)
			}
			start = i + 1
		}
	}
	return names
}

func normalizeWSLOutput(data []byte) string {
	if len(data) >= 2 && (bytes.Equal(data[:2], []byte{0xff, 0xfe}) || nulHeavy(data)) {
		if len(data)%2 != 0 {
			data = data[:len(data)-1]
		}
		start := 0
		if len(data) >= 2 && bytes.Equal(data[:2], []byte{0xff, 0xfe}) {
			start = 2
		}
		units := make([]uint16, 0, (len(data)-start)/2)
		for i := start; i+1 < len(data); i += 2 {
			units = append(units, binary.LittleEndian.Uint16(data[i:i+2]))
		}
		data = []byte(string(utf16.Decode(units)))
	}
	s := string(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}))
	s = bytesToString(bytes.ReplaceAll([]byte(s), []byte("\r\n"), []byte("\n")))
	s = bytesToString(bytes.ReplaceAll([]byte(s), []byte("\r"), []byte("\n")))
	return s
}

func nulHeavy(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	nuls := 0
	for _, b := range data {
		if b == 0 {
			nuls++
		}
	}
	return nuls*4 >= len(data)
}

func containsExact(names []string, expected string) bool {
	return slices.Contains(names, expected)
}
func bytesToString(b []byte) string { return string(b) }
