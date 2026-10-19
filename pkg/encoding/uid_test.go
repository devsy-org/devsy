package encoding

import (
	"regexp"
	"strings"
	"testing"

	"gotest.tools/assert"
)

func TestSafeConcatNameMaxReturnsNamesThatFitUnchanged(t *testing.T) {
	testCases := []struct {
		name   string
		parts  []string
		maxLen int
		want   string
	}{
		{name: "shorter than max", parts: []string{"ws", "dev"}, maxLen: 16, want: "ws-dev"},
		{
			name:   "exactly max",
			parts:  []string{"abcdefgh", "ijklmnop"},
			maxLen: 17,
			want:   "abcdefgh-ijklmnop",
		},
		{name: "single part", parts: []string{"solo"}, maxLen: 4, want: "solo"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, SafeConcatNameMax(tc.parts, tc.maxLen), tc.want)
		})
	}
}

func TestSafeConcatNameMaxTruncatesOverlongNamesToExactlyMax(t *testing.T) {
	testCases := []struct {
		name   string
		parts  []string
		maxLen int
	}{
		{name: "one character over max", parts: []string{"abcdefghijklmnopq"}, maxLen: 16},
		{name: "two long parts", parts: []string{"abcdefghijklmnop", "qrstuvwxyz"}, maxLen: 16},
		{name: "cut lands right after a dash", parts: []string{"abc-defghijklmnop"}, maxLen: 10},
		{name: "many dashes", parts: []string{"a-b-c-d-e-f-g-h-i-j-k"}, maxLen: 12},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, len(SafeConcatNameMax(tc.parts, tc.maxLen)), tc.maxLen)
		})
	}
}

func TestSafeConcatNameMaxSuffixFormatDependsOnCutPoint(t *testing.T) {
	testCases := []struct {
		name    string
		parts   []string
		maxLen  int
		pattern string
	}{
		{
			name:    "cut inside a word appends dash and five digest chars",
			parts:   []string{"abcdefghijklmnopq"},
			maxLen:  16,
			pattern: `^abcdefghij-[0-9a-f]{5}$`,
		},
		{
			name:    "cut after a dash appends six digest chars without a second dash",
			parts:   []string{"abc-defghijklmnop"},
			maxLen:  10,
			pattern: `^abc-[0-9a-f]{6}$`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := SafeConcatNameMax(tc.parts, tc.maxLen)
			assert.Assert(t, regexp.MustCompile(tc.pattern).MatchString(got), "got %q", got)
		})
	}
}

func TestSafeConcatNameMaxDistinguishesOverlongNamesSharingPrefix(t *testing.T) {
	first := SafeConcatNameMax([]string{"abcdefghijklmnop", "a"}, 16)
	second := SafeConcatNameMax([]string{"abcdefghijklmnop", "b"}, 16)

	assert.Assert(t, first != second, "both truncated to %q", first)
}

func TestIsLegacyUIDRecognizesOnlyNonWorkspaceAndNonMachineLengths(t *testing.T) {
	testCases := []struct {
		name   string
		length int
		want   bool
	}{
		{name: "empty", length: 0, want: true},
		{name: "one below workspace uid length", length: WorkspaceUIDLength - 1, want: true},
		{name: "workspace uid length", length: WorkspaceUIDLength, want: false},
		{name: "one above workspace uid length", length: WorkspaceUIDLength + 1, want: true},
		{name: "machine uid length", length: MachineUIDLength, want: false},
		{name: "one above machine uid length", length: MachineUIDLength + 1, want: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, IsLegacyUID(strings.Repeat("a", tc.length)), tc.want)
		})
	}
}

func TestCreateNewUIDAlwaysHasWorkspaceUIDLengthAndIsNotLegacy(t *testing.T) {
	testCases := []struct {
		name    string
		context string
		id      string
	}{
		{name: "no context or id", context: "", id: ""},
		{name: "context only", context: "ctx", id: ""},
		{name: "id only", context: "", id: "id"},
		{name: "context and id", context: "ctx", id: "id"},
		{name: "overlong context", context: strings.Repeat("c", 40), id: "id"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := CreateNewUID(tc.context, tc.id)
			assert.Equal(t, len(got), WorkspaceUIDLength)
			assert.Assert(t, !IsLegacyUID(got), "generated uid %q classified as legacy", got)
		})
	}
}

func TestCreateNewUIDKeepsContextAndIDPrefixWhenTruncated(t *testing.T) {
	got := CreateNewUID("ctx", "id")

	assert.Assert(
		t,
		regexp.MustCompile(`^ctx-id-[0-9a-f]{3}-[0-9a-f]{5}$`).MatchString(got),
		"got %q",
		got,
	)
}

func TestCreateNewUIDShortFormat(t *testing.T) {
	testCases := []struct {
		name    string
		id      string
		pattern string
	}{
		{name: "short id is kept whole", id: "ws", pattern: `^ws-[0-9a-f]{5}$`},
		{
			name:    "long id is truncated to workspace uid length",
			id:      "abcdefghijklmnopqrst",
			pattern: `^abcdefghij-[0-9a-f]{5}$`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := CreateNewUIDShort(tc.id)
			assert.Assert(t, regexp.MustCompile(tc.pattern).MatchString(got), "got %q", got)
		})
	}
}
