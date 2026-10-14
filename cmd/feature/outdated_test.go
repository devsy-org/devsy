package feature

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSemver_BareMajor(t *testing.T) {
	v, err := parseSemver("2")
	require.NoError(t, err)
	assert.Equal(t, uint64(2), v.Major)
	assert.Equal(t, uint64(0), v.Minor)
	assert.Equal(t, uint64(0), v.Patch)
}

func TestParseSemver_MajorMinor(t *testing.T) {
	v, err := parseSemver("1.21")
	require.NoError(t, err)
	assert.Equal(t, uint64(1), v.Major)
	assert.Equal(t, uint64(21), v.Minor)
	assert.Equal(t, uint64(0), v.Patch)
}

func TestParseSemver_Full(t *testing.T) {
	v, err := parseSemver("3.2.1")
	require.NoError(t, err)
	assert.Equal(t, uint64(3), v.Major)
	assert.Equal(t, uint64(2), v.Minor)
	assert.Equal(t, uint64(1), v.Patch)
}

func TestParseSemver_Invalid(t *testing.T) {
	_, err := parseSemver("abc")
	assert.Error(t, err)
}

func TestFindLatestVersion_NewerAvailable(t *testing.T) {
	tags := []string{"1", "2", "3", tagLatest}
	result := findLatestVersion("1", tags)
	assert.Equal(t, "3", result)
}

func TestFindLatestVersion_AlreadyLatest(t *testing.T) {
	tags := []string{"1", "2", "3"}
	result := findLatestVersion("3", tags)
	assert.Empty(t, result)
}

func TestFindLatestVersion_SemverTags(t *testing.T) {
	tags := []string{"1.20", "1.21", "1.22", "1.23"}
	result := findLatestVersion("1.21", tags)
	assert.Equal(t, "1.23", result)
}

func TestFindLatestVersion_FullSemver(t *testing.T) {
	tags := []string{"1.0.0", "1.1.0", "2.0.0", "2.1.0"}
	result := findLatestVersion("1.1.0", tags)
	assert.Equal(t, "2.1.0", result)
}

func TestFindLatestVersion_SkipsLatestTag(t *testing.T) {
	tags := []string{"1", "2", tagLatest}
	result := findLatestVersion("2", tags)
	assert.Empty(t, result)
}

func TestFindLatestVersion_InvalidCurrentTag(t *testing.T) {
	tags := []string{"1", "2", "3"}
	result := findLatestVersion("abc", tags)
	assert.Empty(t, result)
}

func TestFindLatestVersion_MixedValidInvalid(t *testing.T) {
	tags := []string{"1", "abc", "2", "def", "3"}
	result := findLatestVersion("1", tags)
	assert.Equal(t, "3", result)
}

func TestFindLatestVersion_EmptyAndZero(t *testing.T) {
	const zeroVersion = "0.0.0"

	tests := []struct {
		name    string
		current string
		tags    []string
		want    string
	}{
		{
			name:    "nil tags",
			current: "1",
		},
		{
			name:    "empty tags",
			current: "1",
			tags:    []string{},
		},
		{
			name:    "zero version equality",
			current: "0",
			tags:    []string{"0.0", zeroVersion},
		},
		{
			name:    "zero version upgrade",
			current: zeroVersion,
			tags:    []string{"0", "0.0.1", zeroVersion},
			want:    "0.0.1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, findLatestVersion(tt.current, tt.tags))
		})
	}
}

func TestFindLatestVersion_OrderingAndTies(t *testing.T) {
	const (
		majorMinorVersion = "2.0"
		fullVersion       = "2.0.0"
	)

	tests := []struct {
		name    string
		current string
		tags    []string
		want    string
	}{
		{
			name:    "equal normalized current",
			current: "2",
			tags:    []string{"1.9.9", majorMinorVersion, fullVersion},
		},
		{
			name:    "unsorted maximum",
			current: "1",
			tags:    []string{"2", "4.1", "3.9.9", "1", "abc", tagLatest},
			want:    "4.1",
		},
		{
			name:    "normalized tie keeps bare major first",
			current: "1",
			tags:    []string{"2", majorMinorVersion, fullVersion},
			want:    "2",
		},
		{
			name:    "normalized tie keeps full version first",
			current: "1",
			tags:    []string{fullVersion, majorMinorVersion, "2"},
			want:    fullVersion,
		},
		{
			name:    "build metadata tie keeps first tag",
			current: "1",
			tags:    []string{"2.0.0+first", "2.0.0+second", fullVersion},
			want:    "2.0.0+first",
		},
		{
			name:    "build metadata does not upgrade current",
			current: "2.0.0+current",
			tags:    []string{"2.0.0+other", fullVersion},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, findLatestVersion(tt.current, tt.tags))
		})
	}
}

func TestFindLatestVersion_Prereleases(t *testing.T) {
	const releaseVersion = "2.0.0"

	tests := []struct {
		name    string
		current string
		tags    []string
		want    string
	}{
		{
			name:    "prerelease ordering",
			current: "2.0.0-alpha",
			tags:    []string{"2.0.0-beta.2", "2.0.0-beta.10", "2.0.0-beta.1"},
			want:    "2.0.0-beta.10",
		},
		{
			name:    "release outranks prerelease",
			current: "2.0.0-beta",
			tags:    []string{releaseVersion, "2.0.0-rc.1", "2.0.0-alpha"},
			want:    releaseVersion,
		},
		{
			name:    "prerelease below current release",
			current: releaseVersion,
			tags:    []string{"2.0.0-rc.1", "1.9.9", releaseVersion},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, findLatestVersion(tt.current, tt.tags))
		})
	}
}

func TestCheckFeatureVersion_SkipsLocalPath(t *testing.T) {
	_, ok := checkFeatureVersion("./local-feature")
	assert.False(t, ok)
}

func TestCheckFeatureVersion_SkipsRelativePath(t *testing.T) {
	_, ok := checkFeatureVersion("../relative-feature")
	assert.False(t, ok)
}

func TestCheckFeatureVersion_SkipsHTTPURL(t *testing.T) {
	_, ok := checkFeatureVersion("https://example.com/feature.tgz")
	assert.False(t, ok)
}

func TestCheckFeatureVersion_SkipsHTTPURLLowercase(t *testing.T) {
	_, ok := checkFeatureVersion("http://example.com/feature.tgz")
	assert.False(t, ok)
}

func TestIsNonOCIFeature_LocalPath(t *testing.T) {
	assert.True(t, isNonOCIFeature("./my-feature"))
	assert.True(t, isNonOCIFeature("../my-feature"))
}

func TestIsNonOCIFeature_URL(t *testing.T) {
	assert.True(t, isNonOCIFeature("https://example.com/feature.tgz"))
	assert.True(t, isNonOCIFeature("http://example.com/feature.tgz"))
}

func TestIsNonOCIFeature_OCIReference(t *testing.T) {
	assert.False(t, isNonOCIFeature("ghcr.io/devcontainers/features/go:1.21"))
}

func TestParseFeatureTag_ValidOCI(t *testing.T) {
	tag, ok := parseFeatureTag("ghcr.io/devcontainers/features/go:1.21")
	assert.True(t, ok)
	assert.Equal(t, "1.21", tag.TagStr())
}

func TestParseFeatureTag_LatestTag(t *testing.T) {
	_, ok := parseFeatureTag("ghcr.io/devcontainers/features/go:latest")
	assert.False(t, ok)
}

func TestParseFeatureTag_InvalidReference(t *testing.T) {
	_, ok := parseFeatureTag(":::invalid")
	assert.False(t, ok)
}

func TestNewOutdatedCmd_CreatesCommand(t *testing.T) {
	cmd := NewOutdatedCmd(nil)
	assert.Equal(t, "outdated", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
}
