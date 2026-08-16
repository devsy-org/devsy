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
	version := "18.09"
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
	version := "18.09"
	got := BuildPackageList(version, "=1:18.09.0", "")

	pkgs := strings.Split(got, " ")
	s.Equal(
		[]string{PkgDockerCE + "=1:18.09.0", PkgDockerCECLI, PkgContainerd},
		pkgs,
	)
}

func (s *PackagesTestSuite) TestBuildPackageList_ComposeAt2010() {
	version := "20.10"
	got := BuildPackageList(version, "=5:20.10.0", "=5:20.10.0")

	s.Contains(got, PkgDockerCompose)
	s.NotContains(got, PkgDockerBuildx)
}

func (s *PackagesTestSuite) TestBuildPackageList_BuildxAt230() {
	version := "23.0"
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
	got := BuildPackageList("23.0", "", "", PkgDockerRootlessExtras, PkgDockerScan)

	s.True(strings.HasSuffix(got, PkgDockerRootlessExtras+" "+PkgDockerScan))
	s.Contains(got, PkgDockerBuildx)
}

func (s *PackagesTestSuite) TestVersionGte_Table() {
	tests := []struct {
		version string
		target  string
		want    bool
	}{
		{"", "18.09", true},
		{"18.09", "18.09", true},
		{"20.10", "18.09", true},
		{"18.08", "18.09", false},
		{"17.09", "18.09", false},
		{"22.04", "23.0", false},
		{"24.0", "23.0", true},
		{"23", "23.0", true},
		{"23.0", "23.0", true},
		{"20.10-ce", "20.10", true},
		{"18.09-0~debian", "18.09", true},
		{"abc.0", "18.09", false},
		{"1.2.3", "1.2", true},
	}

	for _, tt := range tests {
		got := versionGte(tt.version, tt.target)
		s.Equal(tt.want, got, "versionGte(%q, %q)", tt.version, tt.target)
	}
}
