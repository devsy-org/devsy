package random

import (
	"strings"
	"testing"
)

const lowerASCIILetters = "abcdefghijklmnopqrstuvwxyz"

func TestStringLength(t *testing.T) {
	tests := []struct {
		name string
		n    int
	}{
		{"zero", 0},
		{"one", 1},
		{"typical", 12},
		{"long", 1000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := String(tc.n); len(got) != tc.n {
				t.Fatalf("String(%d) length = %d, want %d", tc.n, len(got), tc.n)
			}
		})
	}
}

func TestStringCharset(t *testing.T) {
	const n = 4096
	got := String(n)
	for _, r := range got {
		if !strings.ContainsRune(lowerASCIILetters, r) {
			t.Fatalf("String(%d) produced rune %q outside lowercase a-z", n, r)
		}
	}
}

func TestStringIsRandom(t *testing.T) {
	const n = 16
	distinct := 0
	const samples = 100
	seen := make(map[string]struct{}, samples)
	for range samples {
		s := String(n)
		if _, ok := seen[s]; !ok {
			distinct++
			seen[s] = struct{}{}
		}
	}
	if distinct < samples-1 {
		t.Fatalf("String(%d) not sufficiently random: %d distinct out of %d", n, distinct, samples)
	}
}

func TestInRangeBounds(t *testing.T) {
	const minVal, maxVal = 13000, 17000
	const samples = 1000
	for range samples {
		got := InRange(minVal, maxVal)
		if got < minVal || got >= maxVal {
			t.Fatalf("InRange(%d, %d) = %d, want [%d, %d)", minVal, maxVal, got, minVal, maxVal)
		}
	}
}

func TestInRangeDegenerateReturnsMin(t *testing.T) {
	tests := []struct {
		name     string
		min, max int
	}{
		{"equal bounds", 13000, 13000},
		{"inverted bounds", 17000, 13000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := InRange(tc.min, tc.max); got != tc.min {
				t.Fatalf("InRange(%d, %d) = %d, want %d", tc.min, tc.max, got, tc.min)
			}
		})
	}
}
