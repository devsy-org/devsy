package cmd

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
