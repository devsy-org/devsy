package platform

import (
	"testing"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	"github.com/stretchr/testify/suite"
)

const (
	ownerTestUser = "alice"
	ownerTestTeam = "devs"
)

type ownerSuite struct {
	suite.Suite
}

func TestOwnerSuite(t *testing.T) {
	suite.Run(t, new(ownerSuite))
}

func (s *ownerSuite) TestIsOwnerMatchesUserName() {
	self := s.self(s.userInfo(ownerTestUser), nil)
	s.True(IsOwner(self, &storagev1.UserOrTeam{User: ownerTestUser}))
}

func (s *ownerSuite) TestIsOwnerRejectsDifferentUser() {
	self := s.self(s.userInfo(ownerTestUser), nil)
	s.False(IsOwner(self, &storagev1.UserOrTeam{User: "bob"}))
}

func (s *ownerSuite) TestIsOwnerMatchesTeamMembership() {
	self := s.self(s.userInfo(ownerTestUser, ownerTestTeam), nil)
	s.True(IsOwner(self, &storagev1.UserOrTeam{Team: ownerTestTeam}))
	s.False(IsOwner(self, &storagev1.UserOrTeam{Team: "other"}))
}

// The acting team owns its resources even when the user is not a member.
func (s *ownerSuite) TestIsOwnerMatchesActingTeam() {
	self := s.self(
		s.userInfo(ownerTestUser),
		&storagev1.EntityInfo{Name: ownerTestTeam},
	)
	s.True(IsOwner(self, &storagev1.UserOrTeam{Team: ownerTestTeam}))
	s.False(IsOwner(self, &storagev1.UserOrTeam{Team: "other"}))
}

func (s *ownerSuite) TestIsOwnerHandlesNilInputs() {
	self := s.self(s.userInfo(ownerTestUser), nil)
	s.False(IsOwner(nil, &storagev1.UserOrTeam{User: ownerTestUser}))
	s.False(IsOwner(self, nil))
	s.False(IsOwner(nil, nil))
}

// An empty user name matches an empty UserOrTeam, since both sides are empty
// strings. Tightening this is an authorization change, so the test pins the
// present behavior rather than asserting it is correct.
func (s *ownerSuite) TestIsOwnerEmptyNamesCurrentlyMatch() {
	self := s.self(&managementv1.UserInfo{}, &storagev1.EntityInfo{})
	s.True(IsOwner(self, &storagev1.UserOrTeam{}))

	// A resolved name does not match, so the match above is the both-empty case.
	named := s.self(s.userInfo(ownerTestUser), nil)
	s.False(IsOwner(named, &storagev1.UserOrTeam{}))
}

func (s *ownerSuite) TestUserIsOwnerHandlesNilUser() {
	s.False(userIsOwner(nil, &storagev1.UserOrTeam{User: ownerTestUser}))
}

func (s *ownerSuite) TestOwnerFilterSetAcceptsKnownValues() {
	for _, tc := range []struct {
		in   string
		want OwnerFilter
	}{
		{"", SelfOwnerFilter},
		{"self", SelfOwnerFilter},
		{"all", AllOwnerFilter},
	} {
		var filter OwnerFilter
		s.Require().NoError(filter.Set(tc.in), "input %q", tc.in)
		s.Equal(tc.want, filter, "input %q", tc.in)
	}
}

func (s *ownerSuite) TestOwnerFilterSetRejectsUnknownValue() {
	var filter OwnerFilter
	err := filter.Set("everything")
	s.Require().Error(err)
	s.Contains(err.Error(), "everything")
}

func (s *ownerSuite) TestOwnerFilterTypeAndString() {
	filter := AllOwnerFilter
	s.Equal("ownerFilter", filter.Type())
	s.Equal("all", filter.String())
}

func (s *ownerSuite) self(
	user *managementv1.UserInfo,
	team *storagev1.EntityInfo,
) *managementv1.Self {
	return &managementv1.Self{
		Status: managementv1.SelfStatus{User: user, Team: team},
	}
}

func (s *ownerSuite) userInfo(teams ...string) *managementv1.UserInfo {
	info := &managementv1.UserInfo{EntityInfo: storagev1.EntityInfo{Name: ownerTestUser}}
	for _, team := range teams {
		info.Teams = append(info.Teams, &storagev1.EntityInfo{Name: team})
	}
	return info
}
