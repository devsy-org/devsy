package platform

import (
	"fmt"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	"github.com/spf13/pflag"
)

func IsOwner(self *managementv1.Self, userOrTeam *storagev1.UserOrTeam) bool {
	if self == nil || userOrTeam == nil {
		return false
	}

	if userIsOwner(self.Status.User, userOrTeam) {
		return true
	}

	// is user owning team?
	return self.Status.Team != nil && self.Status.Team.Name == userOrTeam.Team
}

func userIsOwner(user *managementv1.UserInfo, userOrTeam *storagev1.UserOrTeam) bool {
	if user == nil {
		return false
	}

	// is user owner?
	if user.Name == userOrTeam.User {
		return true
	}

	// is user in owning team?
	for _, team := range user.Teams {
		if team.Name == userOrTeam.Team {
			return true
		}
	}

	return false
}

type OwnerFilter string

const (
	SelfOwnerFilter OwnerFilter = "self"
	AllOwnerFilter  OwnerFilter = "all"
)

var _ pflag.Value = (*OwnerFilter)(nil)

func (s *OwnerFilter) Set(v string) error {
	switch v {
	case "":
		{
			*s = SelfOwnerFilter
			return nil
		}
	case string(SelfOwnerFilter),
		string(AllOwnerFilter):
		{
			*s = OwnerFilter(v)
			return nil
		}
	default:
		return fmt.Errorf("OwnerFilter %s not supported", v)
	}
}

func (s *OwnerFilter) Type() string {
	return "ownerFilter"
}

func (s *OwnerFilter) String() string {
	return string(*s)
}
