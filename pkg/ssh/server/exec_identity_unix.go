//go:build unix

package server

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
)

type execIdentityLookups struct {
	userByName  func(string) (*user.User, error)
	userByID    func(string) (*user.User, error)
	groupByName func(string) (*user.Group, error)
	groups      func(*user.User) ([]string, error)
}

type execCredentials struct {
	uid, gid uint32
	groups   []uint32
	account  *user.User
}

func systemExecIdentityLookups() execIdentityLookups {
	return execIdentityLookups{
		userByName:  user.Lookup,
		userByID:    user.LookupId,
		groupByName: user.LookupGroup,
		groups:      func(account *user.User) ([]string, error) { return account.GroupIds() },
	}
}

func prepareExecIdentity(cmd *exec.Cmd, spec string) (bool, error) {
	return prepareExecIdentityWithLookups(cmd, spec, systemExecIdentityLookups())
}

func prepareExecIdentityWithLookups(
	cmd *exec.Cmd,
	spec string,
	lookups execIdentityLookups,
) (bool, error) {
	identity, handled, err := parseRequestedExecIdentity(spec)
	if !handled || err != nil {
		return handled, err
	}
	if cmd == nil {
		return true, fmt.Errorf("prepare execution identity %q: command is nil", spec)
	}
	credentials, err := resolveExecCredentials(identity, lookups)
	if err != nil {
		return true, fmt.Errorf("resolve execution identity %q: %w", spec, err)
	}
	if !execCredentialsMatchCurrentProcess(credentials) {
		if cmd.SysProcAttr == nil {
			cmd.SysProcAttr = &syscall.SysProcAttr{}
		}
		cmd.SysProcAttr.Credential = &syscall.Credential{
			Uid: credentials.uid, Gid: credentials.gid, Groups: credentials.groups,
		}
	}
	username := strconv.FormatUint(uint64(credentials.uid), 10)
	home := "/"
	if credentials.account != nil {
		username = credentials.account.Username
		home = credentials.account.HomeDir
	}
	cmd.Env = mergeSessionEnvironment(cmd.Environ(), []string{
		"HOME=" + home, "USER=" + username, "LOGNAME=" + username,
	}, nil)
	cmd.Dir = home
	return true, nil
}

func execCredentialsMatchCurrentProcess(credentials *execCredentials) bool {
	if int64(credentials.uid) != int64(os.Geteuid()) ||
		int64(credentials.gid) != int64(os.Getegid()) {
		return false
	}

	groups, err := os.Getgroups()
	if err != nil {
		return false
	}
	return sameEffectiveGroupIDs(credentials.gid, credentials.groups, os.Getegid(), groups)
}

func sameEffectiveGroupIDs(
	expectedPrimary uint32,
	expectedSupplementary []uint32,
	currentPrimary int,
	currentSupplementary []int,
) bool {
	if int64(expectedPrimary) != int64(currentPrimary) {
		return false
	}
	expectedSet := map[int64]struct{}{int64(expectedPrimary): {}}
	for _, group := range expectedSupplementary {
		expectedSet[int64(group)] = struct{}{}
	}

	currentSet := map[int64]struct{}{int64(currentPrimary): {}}
	for _, group := range currentSupplementary {
		if group < 0 {
			return false
		}
		currentSet[int64(group)] = struct{}{}
	}

	return maps.Equal(expectedSet, currentSet)
}

func resolveExecCredentials(
	identity requestedExecIdentity,
	lookups execIdentityLookups,
) (*execCredentials, error) {
	account, err := lookupExecAccount(identity, lookups)
	if err != nil {
		return nil, err
	}
	if account == nil {
		uid, err := parseExecID(identity.user)
		if err != nil {
			return nil, err
		}
		return explicitGroupCredentials(uid, identity, lookups)
	}
	return accountExecCredentials(identity, account, lookups)
}

func lookupExecAccount(
	identity requestedExecIdentity,
	lookups execIdentityLookups,
) (*user.User, error) {
	if !identity.userIsID {
		account, err := lookups.userByName(identity.user)
		if err != nil {
			return nil, fmt.Errorf("look up user %q: %w", identity.user, err)
		}
		return account, nil
	}
	uid, err := parseExecID(identity.user)
	if err != nil {
		return nil, err
	}
	account, _ := lookups.userByID(strconv.FormatUint(uint64(uid), 10))
	if account == nil && !identity.hasGroupSpec {
		return nil, fmt.Errorf("UID %s has no passwd entry; specify UID:GID", identity.user)
	}
	return account, nil
}

func accountExecCredentials(
	identity requestedExecIdentity,
	account *user.User,
	lookups execIdentityLookups,
) (*execCredentials, error) {
	uid, err := parseExecID(account.Uid)
	if err != nil {
		return nil, fmt.Errorf("invalid UID for user %q: %w", identity.user, err)
	}
	gid, err := parseExecID(account.Gid)
	if err != nil {
		return nil, fmt.Errorf("invalid GID for user %q: %w", identity.user, err)
	}
	groups := []uint32{}
	if identity.hasGroupSpec {
		gid, err = resolveGroupID(identity, lookups)
		if err != nil {
			return nil, err
		}
	} else {
		groups, err = supplementaryExecGroups(account, lookups)
		if err != nil {
			return nil, err
		}
	}
	return &execCredentials{uid: uid, gid: gid, groups: groups, account: account}, nil
}

func explicitGroupCredentials(
	uid uint32,
	identity requestedExecIdentity,
	lookups execIdentityLookups,
) (*execCredentials, error) {
	gid, err := resolveGroupID(identity, lookups)
	if err != nil {
		return nil, err
	}
	return &execCredentials{uid: uid, gid: gid, groups: []uint32{}}, nil
}

func resolveGroupID(identity requestedExecIdentity, lookups execIdentityLookups) (uint32, error) {
	if identity.groupIsID {
		return parseExecID(identity.group)
	}
	group, err := lookups.groupByName(identity.group)
	if err != nil {
		return 0, fmt.Errorf("look up group %q: %w", identity.group, err)
	}
	return parseExecID(group.Gid)
}

func supplementaryExecGroups(account *user.User, lookups execIdentityLookups) ([]uint32, error) {
	groupIDs, err := lookups.groups(account)
	if err != nil {
		return nil, fmt.Errorf("look up supplementary groups for %q: %w", account.Username, err)
	}
	groups := make([]uint32, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		id, err := parseExecID(groupID)
		if err != nil {
			return nil, fmt.Errorf("invalid supplementary GID for %q: %w", account.Username, err)
		}
		groups = append(groups, id)
	}
	return groups, nil
}
