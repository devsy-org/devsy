package server

import (
	"fmt"
	"strconv"
	"strings"
)

type requestedExecIdentity struct {
	user         string
	group        string
	userIsID     bool
	groupIsID    bool
	hasGroupSpec bool
}

func parseRequestedExecIdentity(spec string) (requestedExecIdentity, bool, error) {
	if spec == "" {
		return requestedExecIdentity{}, false, nil
	}
	user, group, hasGroup := strings.Cut(spec, ":")
	if invalidExecIdentityParts(user, group, hasGroup) {
		return requestedExecIdentity{}, true, fmt.Errorf("invalid execution identity %q", spec)
	}
	userIsID, err := decimalID(user)
	if err != nil {
		return requestedExecIdentity{}, true, fmt.Errorf(
			"invalid execution identity %q: %w",
			spec,
			err,
		)
	}
	if !userIsID && !hasGroup {
		return requestedExecIdentity{}, false, nil
	}
	groupIsID := false
	if hasGroup {
		groupIsID, err = decimalID(group)
		if err != nil {
			return requestedExecIdentity{}, true, fmt.Errorf(
				"invalid execution identity %q: %w",
				spec,
				err,
			)
		}
	}
	return requestedExecIdentity{
		user: user, group: group, userIsID: userIsID, groupIsID: groupIsID,
		hasGroupSpec: hasGroup,
	}, true, nil
}

func decimalID(value string) (bool, error) {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false, nil
		}
	}
	if value == "" {
		return false, nil
	}
	if _, err := parseExecID(value); err != nil {
		return true, err
	}
	return true, nil
}

func parseExecID(value string) (uint32, error) {
	id, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, err
	}
	// Unix credential syscalls interpret all-bits-one as "leave unchanged".
	if id == 1<<32-1 {
		return 0, fmt.Errorf("ID %q is reserved", value)
	}
	return uint32(id), nil
}

func invalidExecIdentityParts(user, group string, hasGroup bool) bool {
	return user == "" || (hasGroup && (group == "" || strings.Contains(group, ":")))
}
