package ssh

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	ownTestWorkspace = "myws"
	ownTestHost      = ownTestWorkspace + ".devsy"
	ownTestContext   = "default"
	ownTestUID       = "uid-1"
	ownTestUser      = "dev"
	ownTestOldAlias  = "old.devsy"
	ownTestNewAlias  = "new.devsy"
	ownTestThirdName = "third.devsy"
	ownTestOther     = "other"
	ownTestHomeA     = "/h/a"
)

type ownerFixture struct {
	t       *testing.T
	cfgPath string
	homeA   string
	homeB   string
}

func newOwnerFixture(t *testing.T) *ownerFixture {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(config.EnvHome, "")
	sshDir := filepath.Join(home, ".ssh")
	require.NoError(t, os.MkdirAll(sshDir, 0o700))

	return &ownerFixture{
		t:       t,
		cfgPath: filepath.Join(sshDir, "config"),
		homeA:   filepath.Join(home, "devsy-a"),
		homeB:   filepath.Join(home, "devsy-b"),
	}
}

func (f *ownerFixture) owner(home string) Owner {
	return Owner{Home: home, Context: ownTestContext, UID: ownTestUID}
}

func (f *ownerFixture) configure(owner Owner, preferred string) error {
	return ConfigureSSHConfig(SSHConfigParams{
		SSHConfigPath: f.cfgPath,
		Context:       ownTestContext,
		Workspace:     ownTestWorkspace,
		User:          ownTestUser,
		DevsyHome:     owner.Home,
		PreferredHost: preferred,
		Owner:         owner,
	})
}

func (f *ownerFixture) write(content string) {
	f.t.Helper()
	require.NoError(f.t, os.WriteFile(f.cfgPath, []byte(content), 0o600))
}

func (f *ownerFixture) read() string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Clean(f.cfgPath))
	require.NoError(f.t, err)

	return string(data)
}

func (f *ownerFixture) legacyBlock(params addHostParams) string {
	f.t.Helper()
	params.host = ownTestHost
	params.user = ownTestUser
	if params.workspace == "" {
		params.workspace = ownTestWorkspace
	}
	if params.context == "" {
		params.context = ownTestContext
	}
	block, err := addHostSection("", "/usr/bin/devsy", params)
	require.NoError(f.t, err)

	return block
}

func (f *ownerFixture) customHostnameBlock(hostname string) string {
	f.t.Helper()

	return strings.Replace(
		f.legacyBlock(addHostParams{tunnelPort: 2222}),
		"Hostname 127.0.0.1",
		"Hostname "+hostname,
		1,
	)
}

func (f *ownerFixture) blockCount() int {
	return strings.Count(f.read(), MarkerStartPrefix+ownTestHost)
}

func asOwnershipError(t *testing.T, err error) *BlockOwnershipError {
	t.Helper()

	var ownership *BlockOwnershipError
	require.True(t, errors.As(err, &ownership), "expected BlockOwnershipError, got %v", err)

	return ownership
}

func requireOwnershipError(t *testing.T, err error) {
	t.Helper()
	_ = asOwnershipError(t, err)
}

func TestOwnerLineIsStableAndDistinct(t *testing.T) {
	base := Owner{Home: ownTestHomeA, Context: "c", UID: "u"}

	assert.Equal(t, base.Hash(), Owner{Home: ownTestHomeA + "/", Context: "c", UID: "u"}.Hash())
	assert.True(t, strings.HasPrefix(base.Line(), "# Devsy Owner "))
	assert.Len(t, strings.TrimPrefix(base.Line(), OwnerLinePrefix), ownerHashLength)
	assert.NotEqual(t, base.Hash(), Owner{Home: "/h/b", Context: "c", UID: "u"}.Hash())
	assert.NotEqual(t, base.Hash(), Owner{Home: ownTestHomeA, Context: "d", UID: "u"}.Hash())
	assert.NotEqual(t, base.Hash(), Owner{Home: ownTestHomeA, Context: "c", UID: "v"}.Hash())
	assert.NotEqual(t,
		Owner{Home: ownTestHomeA, Context: "bc", UID: "u"}.Hash(),
		Owner{Home: ownTestHomeA, Context: "b", UID: "cu"}.Hash(),
	)
}

func TestNewOwnerUsesDefaultConfigDir(t *testing.T) {
	override := t.TempDir()
	t.Setenv(config.EnvHome, override)

	owner, err := NewOwner(ownTestContext, ownTestUID)
	require.NoError(t, err)

	want, err := config.DefaultPathManager().ConfigDir()
	require.NoError(t, err)
	assert.Equal(t, want, owner.Home)
	assert.Equal(t, ownTestContext, owner.Context)
	assert.Equal(t, ownTestUID, owner.UID)
}

func TestConfigureWritesOwnerLineAfterStartMarker(t *testing.T) {
	f := newOwnerFixture(t)
	owner := f.owner(f.homeA)

	require.NoError(t, f.configure(owner, ownTestOldAlias))

	lines := strings.Split(f.read(), "\n")
	assert.Equal(t, MarkerStartPrefix+ownTestHost, lines[0])
	assert.Equal(t, owner.Line(), lines[1])
	assert.Equal(t, "Host "+ownTestOldAlias+" "+ownTestHost, lines[2])
}

func TestConfigureWithoutOwnerDerivesDefaultHome(t *testing.T) {
	f := newOwnerFixture(t)
	f.homeA = t.TempDir()
	t.Setenv(config.EnvHome, f.homeA)

	require.NoError(t, ConfigureSSHConfig(SSHConfigParams{
		SSHConfigPath: f.cfgPath,
		Context:       ownTestContext,
		Workspace:     ownTestWorkspace,
		User:          ownTestUser,
	}))

	want := Owner{Home: f.homeA, Context: ownTestContext}
	assert.Contains(t, f.read(), want.Line())
}

func TestConfigureSameOwnerReplacesBlock(t *testing.T) {
	f := newOwnerFixture(t)
	owner := f.owner(f.homeA)

	require.NoError(t, f.configure(owner, ""))
	require.NoError(t, f.configure(owner, ownTestOldAlias))

	assert.Equal(t, 1, f.blockCount())
	assert.Contains(t, f.read(), "Host "+ownTestOldAlias+" "+ownTestHost)
}

func TestConfigureNeverReplacesDifferentOwnerBlock(t *testing.T) {
	cases := map[string]func(*ownerFixture) Owner{
		"different home": func(f *ownerFixture) Owner { return f.owner(f.homeB) },
		"different uid": func(f *ownerFixture) Owner {
			o := f.owner(f.homeA)
			o.UID = "uid-2"

			return o
		},
		"different context": func(f *ownerFixture) Owner {
			o := f.owner(f.homeA)
			o.Context = ownTestOther

			return o
		},
	}

	for name, intruder := range cases {
		t.Run(name, func(t *testing.T) {
			f := newOwnerFixture(t)
			require.NoError(t, f.configure(f.owner(f.homeA), ownTestOldAlias))
			before := f.read()

			err := ConfigureSSHConfig(SSHConfigParams{
				SSHConfigPath: f.cfgPath,
				Context:       ownTestContext,
				Workspace:     ownTestWorkspace,
				User:          ownTestUser,
				Owner:         intruder(f),
			})

			ownership := asOwnershipError(t, err)
			assert.Equal(t, ownTestHost, ownership.Host)
			assert.Equal(t, f.cfgPath, ownership.File)
			assert.Equal(t, before, f.read())
		})
	}
}

func TestConfigureClaimsLegacyBlockWithMatchingProxyEvidence(t *testing.T) {
	f := newOwnerFixture(t)
	owner := f.owner(f.homeA)
	f.write(f.legacyBlock(addHostParams{devsyHome: f.homeA}) + "\nHost keep\n  User x\n")

	require.NoError(t, f.configure(owner, ""))

	assert.Equal(t, 1, f.blockCount())
	assert.Contains(t, f.read(), owner.Line())
	assert.Contains(t, f.read(), "Host keep")
}

func TestConfigureClaimsLegacyBlockWithImpliedHome(t *testing.T) {
	f := newOwnerFixture(t)
	f.write(f.legacyBlock(addHostParams{}))

	err := ConfigureSSHConfig(SSHConfigParams{
		SSHConfigPath: f.cfgPath,
		Context:       ownTestContext,
		Workspace:     ownTestWorkspace,
		User:          ownTestUser,
		Owner:         f.owner(f.homeA),
	})

	require.NoError(t, err)
	assert.Contains(t, f.read(), f.owner(f.homeA).Line())
}

func TestConfigureRejectsLegacyBlockWithoutMatchingEvidence(t *testing.T) {
	cases := map[string]addHostParams{
		"custom command":   {command: "ssh -W %h:%p jump"},
		"other context":    {context: ownTestOther, devsyHome: ""},
		"other workspace":  {workspace: "different"},
		"other home":       {devsyHome: "/somewhere/else"},
		"implied vs given": {},
	}

	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			f := newOwnerFixture(t)
			f.write(f.legacyBlock(params))
			before := f.read()

			err := f.configure(f.owner(f.homeA), "")

			ownership := asOwnershipError(t, err)
			assert.Equal(t, ownTestHost, ownership.Host)
			assert.Equal(t, f.cfgPath, ownership.File)
			assert.Contains(t, err.Error(), ownTestHost)
			assert.Contains(t, err.Error(), f.cfgPath)
			assert.Equal(t, before, f.read())
		})
	}
}

func TestConfigureRejectsLegacyTunnelBlockWithCustomHostname(t *testing.T) {
	f := newOwnerFixture(t)
	f.write(f.customHostnameBlock("10.0.0.5"))
	before := f.read()

	err := f.configure(f.owner(f.homeA), "")

	ownership := asOwnershipError(t, err)
	assert.Equal(t, ownTestHost, ownership.Host)
	assert.Equal(t, before, f.read())
}

func TestRemoveFromConfigForOwnerRemovesOnlyOwnedBlock(t *testing.T) {
	f := newOwnerFixture(t)
	ownerA := f.owner(f.homeA)
	require.NoError(t, f.configure(ownerA, ""))
	f.write(f.read() + "\nHost keep\n  User x\n")

	err := RemoveFromConfigForOwner(f.owner(f.homeB), ownTestWorkspace, f.cfgPath, "")
	requireOwnershipError(t, err)
	assert.Equal(t, 1, f.blockCount())

	require.NoError(t, RemoveFromConfigForOwner(ownerA, ownTestWorkspace, f.cfgPath, ""))
	assert.Zero(t, f.blockCount())
	assert.Contains(t, f.read(), "Host keep")
}

func TestRemoveFromConfigForOwnerKeepsBlocksOfOtherWorkspaces(t *testing.T) {
	f := newOwnerFixture(t)
	ownerA := f.owner(f.homeA)
	require.NoError(t, f.configure(ownerA, ""))
	require.NoError(t, ConfigureSSHConfig(SSHConfigParams{
		SSHConfigPath: f.cfgPath,
		Context:       ownTestContext,
		Workspace:     ownTestOther,
		User:          ownTestUser,
		Owner:         Owner{Home: f.homeB, Context: ownTestContext, UID: "uid-9"},
	}))

	require.NoError(t, RemoveFromConfigForOwner(ownerA, ownTestWorkspace, f.cfgPath, ""))

	assert.Zero(t, f.blockCount())
	assert.Contains(t, f.read(), MarkerStartPrefix+"other.devsy")
}

func TestRemoveFromConfigForOwnerClaimsLegacyBlockWithEvidence(t *testing.T) {
	f := newOwnerFixture(t)
	f.write(f.legacyBlock(addHostParams{devsyHome: f.homeA}))

	require.NoError(t, RemoveFromConfigForOwner(f.owner(f.homeA), ownTestWorkspace, f.cfgPath, ""))
	assert.Zero(t, f.blockCount())
}

func TestRemoveFromConfigForOwnerRejectsLegacyBlockWithoutEvidence(t *testing.T) {
	f := newOwnerFixture(t)
	f.write(f.customHostnameBlock("10.0.0.5"))
	before := f.read()

	err := RemoveFromConfigForOwner(f.owner(f.homeA), ownTestWorkspace, f.cfgPath, "")

	requireOwnershipError(t, err)
	assert.Equal(t, before, f.read())
}

func TestLegacyTunnelBlockIsClaimedForEveryHostname(t *testing.T) {
	for _, hostname := range []string{"127.0.0.1", "localhost", "LOCALHOST", "::1"} {
		t.Run(hostname, func(t *testing.T) {
			f := newOwnerFixture(t)
			owner := f.owner(f.homeA)
			f.write(f.customHostnameBlock(hostname) + "\nHost keep\n  User x\n")

			require.NoError(t, f.configure(owner, ""))

			assert.Equal(t, 1, f.blockCount())
			assert.Contains(t, f.read(), owner.Line())
			assert.Contains(t, f.read(), "ProxyCommand")
			assert.NotContains(t, f.read(), "Port 2222")
			assert.Contains(t, f.read(), "Host keep")
		})
	}
}

func TestConfigureReplacesLegacyTunnelBlockAndAddsOwnerLine(t *testing.T) {
	f := newOwnerFixture(t)
	owner := f.owner(f.homeA)
	f.write(f.legacyBlock(addHostParams{tunnelPort: 2222}))
	assert.NotContains(t, f.read(), OwnerLinePrefix)

	require.NoError(t, f.configure(owner, ""))

	assert.Equal(t, 1, f.blockCount())
	assert.Equal(t, 1, strings.Count(f.read(), owner.Line()))
}

func TestLegacyTunnelBlockWithoutPortIsNotClaimed(t *testing.T) {
	f := newOwnerFixture(t)
	f.write(MarkerStartPrefix + ownTestHost + "\nHost " + ownTestHost +
		"\n  Hostname 127.0.0.1\n" + MarkerEndPrefix + ownTestHost + "\n")
	before := f.read()

	requireOwnershipError(t, f.configure(f.owner(f.homeA), ""))
	assert.Equal(t, before, f.read())
}

func TestRemoveFromConfigForOwnerClaimsLegacyTunnelBlock(t *testing.T) {
	f := newOwnerFixture(t)
	f.write(f.legacyBlock(addHostParams{tunnelPort: 2222}))

	require.NoError(t, RemoveFromConfigForOwner(f.owner(f.homeA), ownTestWorkspace, f.cfgPath, ""))
	assert.Zero(t, f.blockCount())
}

func TestReplaceHostAliasesForOwnerClaimsLegacyTunnelBlock(t *testing.T) {
	f := newOwnerFixture(t)
	f.write(f.legacyBlock(addHostParams{tunnelPort: 2222}))

	require.NoError(t, f.editAlias(f.owner(f.homeA), &aliasRecord{}, ownTestNewAlias))
	assert.Equal(t, 1, f.blockCount())
	assert.Contains(t, f.read(), "Host "+ownTestNewAlias+" "+ownTestHost)
}

func TestFindHostConflictsForOwnerExcludesLegacyTunnelBlock(t *testing.T) {
	f := newOwnerFixture(t)
	f.write(f.legacyBlock(addHostParams{tunnelPort: 2222, preferredHost: ownTestOldAlias}))

	query := HostConflictQuery{
		ConfigPath:    f.cfgPath,
		Hosts:         []string{ownTestOldAlias},
		CanonicalHost: ownTestHost,
		Owner:         f.owner(f.homeA),
	}
	conflicts, err := FindHostConflictsForOwner(query)
	require.NoError(t, err)
	assert.Empty(t, conflicts)
}

func TestLegacyRemoveFromConfigIgnoresOwnership(t *testing.T) {
	f := newOwnerFixture(t)
	require.NoError(t, f.configure(f.owner(f.homeB), ""))

	require.NoError(t, RemoveFromConfig(ownTestWorkspace, f.cfgPath, ""))
	assert.Zero(t, f.blockCount())
}

func TestReplaceHostAliasesForOwnerRefusesForeignBlock(t *testing.T) {
	f := newOwnerFixture(t)
	require.NoError(t, f.configure(f.owner(f.homeB), ownTestOldAlias))
	before := f.read()
	committed := 0

	updated, err := ReplaceHostAliasesForOwner(
		AliasReplacement{
			ConfigPath:    f.cfgPath,
			CanonicalHost: ownTestHost,
			PreferredHost: ownTestNewAlias,
		},
		f.owner(f.homeA),
		countingCommit(&committed),
	)

	requireOwnershipError(t, err)
	assert.False(t, updated)
	assert.Zero(t, committed)
	assert.Equal(t, before, f.read())
}

func TestReplaceHostAliasesForOwnerRewritesOwnedBlock(t *testing.T) {
	f := newOwnerFixture(t)
	owner := f.owner(f.homeA)
	require.NoError(t, f.configure(owner, ownTestOldAlias))
	committed := 0

	updated, err := ReplaceHostAliasesForOwner(
		AliasReplacement{
			ConfigPath:    f.cfgPath,
			CanonicalHost: ownTestHost,
			PreferredHost: ownTestNewAlias,
		},
		owner,
		countingCommit(&committed),
	)

	require.NoError(t, err)
	assert.True(t, updated)
	assert.Equal(t, 1, committed)
	assert.Contains(t, f.read(), "Host "+ownTestNewAlias+" "+ownTestHost)
	assert.Contains(t, f.read(), owner.Line())
}

func TestFindHostConflictsForOwnerExcludesOnlyOwnedBlock(t *testing.T) {
	f := newOwnerFixture(t)
	ownerA := f.owner(f.homeA)
	require.NoError(t, f.configure(ownerA, ownTestOldAlias))

	query := HostConflictQuery{
		ConfigPath:    f.cfgPath,
		Hosts:         []string{ownTestOldAlias},
		CanonicalHost: ownTestHost,
		Owner:         ownerA,
	}
	conflicts, err := FindHostConflictsForOwner(query)
	require.NoError(t, err)
	assert.Empty(t, conflicts)

	query.Owner = f.owner(f.homeB)
	conflicts, err = FindHostConflictsForOwner(query)
	require.NoError(t, err)
	assert.Equal(t, []HostConflict{{Host: ownTestOldAlias, File: f.cfgPath}}, conflicts)
}

func TestFindHostConflictsForOwnerExcludesEvidenceBackedLegacyBlock(t *testing.T) {
	f := newOwnerFixture(t)
	f.write(f.legacyBlock(addHostParams{devsyHome: f.homeA, preferredHost: ownTestOldAlias}))

	query := HostConflictQuery{
		ConfigPath:    f.cfgPath,
		Hosts:         []string{ownTestOldAlias},
		CanonicalHost: ownTestHost,
		Owner:         f.owner(f.homeA),
	}
	conflicts, err := FindHostConflictsForOwner(query)
	require.NoError(t, err)
	assert.Empty(t, conflicts)

	query.Owner = f.owner(f.homeB)
	conflicts, err = FindHostConflictsForOwner(query)
	require.NoError(t, err)
	assert.Len(t, conflicts, 1)
}

func TestTwoHomesWithSameContextAndUIDCannotTouchEachOther(t *testing.T) {
	f := newOwnerFixture(t)
	ownerA := f.owner(f.homeA)
	ownerB := f.owner(f.homeB)
	require.NoError(t, f.configure(ownerA, ownTestOldAlias))
	before := f.read()

	requireOwnershipError(t, f.configure(ownerB, ownTestNewAlias))
	requireOwnershipError(t, RemoveFromConfigForOwner(ownerB, ownTestWorkspace, f.cfgPath, ""))
	_, err := ReplaceHostAliasesForOwner(
		AliasReplacement{
			ConfigPath:    f.cfgPath,
			CanonicalHost: ownTestHost,
			PreferredHost: ownTestNewAlias,
		},
		ownerB,
		nil,
	)
	requireOwnershipError(t, err)

	assert.Equal(t, before, f.read())
}

type aliasRecord struct {
	alias string
}

func (f *ownerFixture) editAlias(owner Owner, record *aliasRecord, alias string) error {
	_, err := ReplaceHostAliasesForOwner(
		AliasReplacement{
			ConfigPath:    f.cfgPath,
			CanonicalHost: ownTestHost,
			PreferredHost: alias,
		},
		owner,
		func() error {
			record.alias = alias

			return nil
		},
	)

	return err
}

func (f *ownerFixture) crashAfterBlockRewrite(owner Owner) {
	_, err := ReplaceHostAliasesForOwner(
		AliasReplacement{
			ConfigPath:    f.cfgPath,
			CanonicalHost: ownTestHost,
			PreferredHost: ownTestNewAlias,
		},
		owner,
		nil,
	)
	require.NoError(f.t, err)
}

func TestReconcileConfigureConvergesBlockToCommittedAlias(t *testing.T) {
	f := newOwnerFixture(t)
	owner := f.owner(f.homeA)
	record := &aliasRecord{alias: ownTestOldAlias}
	require.NoError(t, f.configure(owner, record.alias))

	f.crashAfterBlockRewrite(owner)
	assert.Contains(t, f.read(), "Host "+ownTestNewAlias+" "+ownTestHost)
	assert.Equal(t, ownTestOldAlias, record.alias)

	require.NoError(t, f.configure(owner, record.alias))

	assert.Equal(t, 1, f.blockCount())
	assert.Contains(t, f.read(), "Host "+ownTestOldAlias+" "+ownTestHost)
	assert.NotContains(t, f.read(), ownTestNewAlias)
}

func TestReconcileAliasEditConvergesBlockAndRecord(t *testing.T) {
	for _, target := range []string{ownTestNewAlias, ownTestThirdName} {
		t.Run(target, func(t *testing.T) {
			f := newOwnerFixture(t)
			owner := f.owner(f.homeA)
			record := &aliasRecord{alias: ownTestOldAlias}
			require.NoError(t, f.configure(owner, record.alias))
			f.crashAfterBlockRewrite(owner)

			require.NoError(t, f.editAlias(owner, record, target))

			assert.Equal(t, target, record.alias)
			assert.Equal(t, 1, f.blockCount())
			assert.Contains(t, f.read(), "Host "+target+" "+ownTestHost)
			assert.Contains(t, f.read(), owner.Line())
		})
	}
}

func TestReconcileAliasEditToAlreadyRewrittenHostStillCommits(t *testing.T) {
	f := newOwnerFixture(t)
	owner := f.owner(f.homeA)
	record := &aliasRecord{alias: ownTestOldAlias}
	require.NoError(t, f.configure(owner, record.alias))
	f.crashAfterBlockRewrite(owner)

	require.NoError(t, f.editAlias(owner, record, ownTestNewAlias))

	assert.Equal(t, ownTestNewAlias, record.alias)
	assert.Contains(t, f.read(), "Host "+ownTestNewAlias+" "+ownTestHost)
}

func TestReconcileDeleteRemovesOnlyOwnedBlock(t *testing.T) {
	f := newOwnerFixture(t)
	owner := f.owner(f.homeA)
	require.NoError(t, f.configure(owner, ownTestOldAlias))
	f.crashAfterBlockRewrite(owner)
	f.write("Host before\n  User b\n" + f.read() + "\nHost after\n  User a\n")

	require.NoError(t, RemoveFromConfigForOwner(owner, ownTestWorkspace, f.cfgPath, ""))

	assert.Zero(t, f.blockCount())
	assert.NotContains(t, f.read(), ownTestNewAlias)
	assert.Contains(t, f.read(), "Host before")
	assert.Contains(t, f.read(), "Host after")
}
