package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

const absoluteReferenceAliasKey = "ALIAS"

func absoluteReferenceArchiveBinary(t *testing.T, content []byte) *provider.ProviderBinary {
	t.Helper()
	server := compatibilityArchiveServer(
		t,
		singleExecutableUpdateArchive(t, content),
	)
	binary := updateTestBinary(server.URL+"/provider.tar", content)
	binary.ArchivePath = updateRuntimeName
	return binary
}

func TestProviderUpdateAbsoluteReferenceUsesFinalStagedTarget(t *testing.T) {
	t.Run("unchanged managed target succeeds", testUnchangedManagedAbsoluteReference)
	t.Run("changed managed target with matching new checksum succeeds", func(t *testing.T) {
		assertManagedAbsoluteReplacementAccepted(t, false, true)
	})
	t.Run("replaced managed symlink with matching new checksum succeeds", func(t *testing.T) {
		assertManagedAbsoluteReplacementAccepted(t, true, true)
	})
	t.Run("new managed target with matching checksum succeeds", func(t *testing.T) {
		assertManagedAbsoluteReplacementAccepted(t, false, false)
	})

	t.Run("changed managed target rejects and preserves live state", func(t *testing.T) {
		assertChangedManagedAbsoluteReferenceRejected(t, false)
	})

	t.Run("contained symlink follows changed staged target", func(t *testing.T) {
		assertChangedManagedAbsoluteReferenceRejected(t, true)
	})

	t.Run("external symlink into managed tree fails closed", testExternalSymlinkIntoManagedTree)
	t.Run(
		"case-variant managed path cannot bypass validation",
		testCaseVariantManagedAbsoluteReference,
	)

	t.Run(
		"outside reference remains untouched while managed tree updates",
		testOutsideAbsoluteReferenceUnchanged,
	)
}

func assertManagedAbsoluteReplacementAccepted(t *testing.T, throughSymlink, seedTarget bool) {
	t.Helper()
	f := newPreservationFixture(t)
	oldContent := []byte("old managed executable")
	newContent := []byte("new managed executable")
	if seedTarget {
		f.seedBinary("B", oldContent)
	}
	aliasPath := f.binaryPath("B")
	aliasKey := absoluteReferenceAliasKey
	replacementKey := "B"
	if throughSymlink {
		requireProviderUpdateSymlinks(t)
		aliasKey = "REFERENCE"
		replacementKey = absoluteReferenceAliasKey
		aliasPath = f.binaryPath(absoluteReferenceAliasKey)
		require.NoError(t, os.MkdirAll(filepath.Dir(aliasPath), 0o700))
		require.NoError(t, os.Symlink(filepath.Join("..", "b", updateRuntimeName), aliasPath))
	}
	alias := updateTestBinary(aliasPath, newContent)
	err := f.update(map[string][]*provider.ProviderBinary{
		aliasKey:       {alias},
		replacementKey: {absoluteReferenceArchiveBinary(t, newContent)},
	})
	require.NoError(t, err)
	// #nosec G304 -- aliasPath is confined to this fixture's owned provider tree.
	actual, err := os.ReadFile(aliasPath)
	require.NoError(t, err)
	require.Equal(t, newContent, actual)
	info, err := os.Lstat(aliasPath)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular())
	stored, err := provider.LoadProviderConfig(testDefaultContext, f.name)
	require.NoError(t, err)
	require.Equal(t, aliasPath, stored.Binaries[aliasKey][0].Path)
	require.Equal(t, alias.Checksum, stored.Binaries[aliasKey][0].Checksum)
	if throughSymlink {
		preserved, readErr := os.ReadFile(f.binaryPath("B"))
		require.NoError(t, readErr)
		require.Equal(t, oldContent, preserved)
	}
	f.assertAssociation()
	assertNoUpdateStaging(t, f)
}

func testUnchangedManagedAbsoluteReference(t *testing.T) {
	f := newPreservationFixture(t)
	managedContent := []byte("unchanged managed executable")
	f.seedBinary("B", managedContent)

	err := f.update(map[string][]*provider.ProviderBinary{
		absoluteReferenceAliasKey: {updateTestBinary(f.binaryPath("B"), managedContent)},
		"C": {
			absoluteReferenceArchiveBinary(t, []byte("updated independent executable")),
		},
	})
	require.NoError(t, err)
	actual, err := os.ReadFile(f.binaryPath("B"))
	require.NoError(t, err)
	require.Equal(t, managedContent, actual)
	stored, err := provider.LoadProviderConfig(testDefaultContext, f.name)
	require.NoError(t, err)
	require.Equal(t, f.binaryPath("B"), stored.Binaries[absoluteReferenceAliasKey][0].Path)
	f.assertAssociation()
	assertNoUpdateStaging(t, f)
}

func testExternalSymlinkIntoManagedTree(t *testing.T) {
	requireProviderUpdateSymlinks(t)
	f := newPreservationFixture(t)
	managedContent := []byte("managed executable")
	f.seedBinary("B", managedContent)
	externalAlias := filepath.Join(t.TempDir(), "external-alias")
	require.NoError(t, os.Symlink(f.binaryPath("B"), externalAlias))

	err := f.update(map[string][]*provider.ProviderBinary{
		absoluteReferenceAliasKey: {updateTestBinary(externalAlias, managedContent)},
		"B": {
			absoluteReferenceArchiveBinary(t, []byte("updated managed executable")),
		},
	})
	require.ErrorContains(t, err, "external alias; unsupported")
	f.assertFailedPreservation()
	assertNoUpdateStaging(t, f)
	info, statErr := os.Lstat(externalAlias)
	require.NoError(t, statErr)
	require.NotZero(t, info.Mode()&os.ModeSymlink)
}

func testOutsideAbsoluteReferenceUnchanged(t *testing.T) {
	f := newPreservationFixture(t)
	outsideContent := []byte("independently owned executable")
	outsidePath := filepath.Join(t.TempDir(), "outside-runtime")
	f.put(outsidePath, outsideContent)
	f.seedBinary("B", []byte("old managed executable"))

	err := f.update(map[string][]*provider.ProviderBinary{
		"OUTSIDE": {updateTestBinary(outsidePath, outsideContent)},
		"B":       {absoluteReferenceArchiveBinary(t, []byte("new managed executable"))},
	})
	require.NoError(t, err)
	// #nosec G304 -- outsidePath was created inside this test's owned temp directory.
	actual, err := os.ReadFile(outsidePath)
	require.NoError(t, err)
	require.Equal(t, outsideContent, actual)
	stored, err := provider.LoadProviderConfig(testDefaultContext, f.name)
	require.NoError(t, err)
	require.Equal(t, outsidePath, stored.Binaries["OUTSIDE"][0].Path)
	f.assertAssociation()
	assertNoUpdateStaging(t, f)
}

func testCaseVariantManagedAbsoluteReference(t *testing.T) {
	f := newPreservationFixture(t)
	oldContent := []byte("old managed executable")
	f.seedBinary("B", oldContent)
	caseVariantRoot := strings.ToUpper(f.binaries)
	liveInfo, err := os.Stat(f.binaries)
	require.NoError(t, err)
	variantInfo, err := os.Stat(caseVariantRoot)
	if os.IsNotExist(err) {
		t.Skip("case-variant path does not resolve on this filesystem")
	}
	require.NoError(t, err)
	if !os.SameFile(liveInfo, variantInfo) {
		t.Skip("case-variant path is a different directory on this filesystem")
	}
	variantPath := filepath.Join(caseVariantRoot, "b", updateRuntimeName)
	err = f.update(map[string][]*provider.ProviderBinary{
		absoluteReferenceAliasKey: {updateTestBinary(variantPath, oldContent)},
		"B": {
			absoluteReferenceArchiveBinary(t, []byte("new managed executable")),
		},
	})
	require.Error(t, err)
	f.assertFailedPreservation()
	assertNoUpdateStaging(t, f)
}

func assertChangedManagedAbsoluteReferenceRejected(t *testing.T, throughSymlink bool) {
	t.Helper()
	f := newPreservationFixture(t)
	oldContent := []byte("old managed executable")
	newContent := []byte("new managed executable")
	f.seedBinary("B", oldContent)
	aliasPath := f.binaryPath("B")
	aliasKey := absoluteReferenceAliasKey
	replacementKey := "B"
	if throughSymlink {
		requireProviderUpdateSymlinks(t)
		aliasKey = "REFERENCE"
		replacementKey = absoluteReferenceAliasKey
		aliasPath = f.binaryPath(absoluteReferenceAliasKey)
		require.NoError(t, os.MkdirAll(filepath.Dir(aliasPath), 0o700))
		require.NoError(t, os.Symlink(filepath.Join("..", "b", updateRuntimeName), aliasPath))
	}

	updated := map[string][]*provider.ProviderBinary{
		aliasKey:       {updateTestBinary(aliasPath, oldContent)},
		replacementKey: {absoluteReferenceArchiveBinary(t, newContent)},
	}
	err := f.update(updated)
	require.ErrorContains(t, err, "absolute provider binary checksum mismatch")
	f.assertFailedPreservation()
	assertNoUpdateStaging(t, f)
	if throughSymlink {
		info, statErr := os.Lstat(aliasPath)
		require.NoError(t, statErr)
		require.NotZero(t, info.Mode()&os.ModeSymlink)
		target, readErr := os.Readlink(aliasPath)
		require.NoError(t, readErr)
		require.Equal(t, filepath.Join("..", "b", updateRuntimeName), target)
	}
}
