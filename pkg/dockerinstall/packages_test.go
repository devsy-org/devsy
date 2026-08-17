package dockerinstall

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type PackagesTestSuite struct {
	suite.Suite
}

func TestPackagesSuite(t *testing.T) {
	suite.Run(t, new(PackagesTestSuite))
}

func (s *PackagesTestSuite) TestBuildPackageList_PreCLI() {
	version := "17.09"
	got := BuildPackageList(version, "=1:17.09.0", "", "docker-ce-rootless-extras")

	pkgs := strings.Split(got, " ")
	s.Equal([]string{PkgDockerCE + "=1:17.09.0", "docker-ce-rootless-extras"}, pkgs)
	s.NotContains(got, PkgDockerCECLI)
	s.NotContains(got, PkgContainerd)
	s.NotContains(got, PkgDockerCompose)
	s.NotContains(got, PkgDockerBuildx)
}

func (s *PackagesTestSuite) TestBuildPackageList_CLIWithPinnedCLI() {
	version := ubuntuRelease1809
	got := BuildPackageList(version, "=1:18.09.0", "=1:18.09.0-3")

	pkgs := strings.Split(got, " ")
	s.Equal(
		[]string{
			PkgDockerCE + "=1:18.09.0",
			PkgDockerCECLI + "=1:18.09.0-3",
			PkgContainerd,
		},
		pkgs,
	)
	s.NotContains(got, PkgDockerCompose)
	s.NotContains(got, PkgDockerBuildx)
}

func (s *PackagesTestSuite) TestBuildPackageList_CLIWithoutPinnedCLI() {
	version := ubuntuRelease1809
	got := BuildPackageList(version, "=1:18.09.0", "")

	pkgs := strings.Split(got, " ")
	s.Equal(
		[]string{PkgDockerCE + "=1:18.09.0", PkgDockerCECLI, PkgContainerd},
		pkgs,
	)
}

func (s *PackagesTestSuite) TestBuildPackageList_ComposeAt2010() {
	version := ubuntuRelease2010
	got := BuildPackageList(version, "=5:20.10.0", "=5:20.10.0")

	s.Contains(got, PkgDockerCompose)
	s.NotContains(got, PkgDockerBuildx)
}

func (s *PackagesTestSuite) TestBuildPackageList_BuildxAt230() {
	version := ubuntuRelease230
	got := BuildPackageList(version, "=5:23.0.0", "=5:23.0.0")

	pkgs := strings.Split(got, " ")
	s.Equal(
		[]string{
			PkgDockerCE + "=5:23.0.0",
			PkgDockerCECLI + "=5:23.0.0",
			PkgContainerd,
			PkgDockerCompose,
			PkgDockerBuildx,
		},
		pkgs,
	)
}

func (s *PackagesTestSuite) TestBuildPackageList_EmptyVersionEnablesAllFeatures() {
	got := BuildPackageList("", "", "")

	pkgs := strings.Split(got, " ")
	s.Equal(
		[]string{
			PkgDockerCE,
			PkgDockerCECLI,
			PkgContainerd,
			PkgDockerCompose,
			PkgDockerBuildx,
		},
		pkgs,
	)
}

func (s *PackagesTestSuite) TestBuildPackageList_ExtraPackagesAppended() {
	got := BuildPackageList(ubuntuRelease230, "", "", PkgDockerRootlessExtras, PkgDockerScan)

	s.True(strings.HasSuffix(got, PkgDockerRootlessExtras+" "+PkgDockerScan))
	s.Contains(got, PkgDockerBuildx)
}

func (s *PackagesTestSuite) TestVersionGte_Table() {
	tests := []struct {
		version string
		target  string
		want    bool
	}{
		{"", ubuntuRelease1809, true},
		{ubuntuRelease1809, ubuntuRelease1809, true},
		{ubuntuRelease2010, ubuntuRelease1809, true},
		{"18.08", ubuntuRelease1809, false},
		{"17.09", ubuntuRelease1809, false},
		{ubuntuRelease2204, ubuntuRelease230, false},
		{"24.0", ubuntuRelease230, true},
		{"23", ubuntuRelease230, true},
		{ubuntuRelease230, ubuntuRelease230, true},
		{"20.10-ce", ubuntuRelease2010, true},
		{"18.09-0~debian", ubuntuRelease1809, true},
		{"abc.0", ubuntuRelease1809, false},
		{"1.2.3", "1.2", true},
	}

	for _, tt := range tests {
		got := versionGte(tt.version, tt.target)
		s.Equal(tt.want, got, "versionGte(%q, %q)", tt.version, tt.target)
	}
}
