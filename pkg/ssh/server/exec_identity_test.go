//go:build unix

package server

import (
	"errors"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseRequestedExecIdentity(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		handled bool
		wantErr bool
	}{
		{name: "empty preserves default", spec: ""},
		{name: "named user preserves su", spec: "vscode"},
		{name: "numeric UID", spec: "1000", handled: true},
		{name: "numeric UID and GID", spec: "1000:2000", handled: true},
		{name: "named group", spec: "1000:staff", handled: true},
		{name: "empty user", spec: ":1000", handled: true, wantErr: true},
		{name: "empty group", spec: "1000:", handled: true, wantErr: true},
		{name: "multiple separators", spec: "1000:2000:3000", handled: true, wantErr: true},
		{name: "overflow UID", spec: "4294967296", handled: true, wantErr: true},
		{name: "overflow GID", spec: "1000:4294967296", handled: true, wantErr: true},
		{name: "reserved UID sentinel", spec: "4294967295:1000", handled: true, wantErr: true},
		{name: "reserved GID sentinel", spec: "1000:4294967295", handled: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, handled, err := parseRequestedExecIdentity(tt.spec)
			require.Equal(t, tt.handled, handled)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestResolveExecCredentials(t *testing.T) {
	account, lookups := execIdentityTestLookups()
	identity := func(spec string) requestedExecIdentity {
		t.Helper()
		parsed, handled, err := parseRequestedExecIdentity(spec)
		require.NoError(t, err)
		require.True(t, handled)
		return parsed
	}

	t.Run("UID only uses account primary and supplementary groups", func(t *testing.T) {
		got, err := resolveExecCredentials(identity("1000"), lookups)
		require.NoError(t, err)
		require.Equal(t, uint32(1000), got.uid)
		require.Equal(t, uint32(2000), got.gid)
		require.Equal(t, []uint32{2001, 2002}, got.groups)
		require.Same(t, account, got.account)
	})

	t.Run("explicit numeric group clears supplementary groups", func(t *testing.T) {
		got, err := resolveExecCredentials(identity("1000:3000"), lookups)
		require.NoError(t, err)
		require.Equal(t, uint32(3000), got.gid)
		require.Empty(t, got.groups)
	})

	t.Run("unknown UID is allowed with explicit GID", func(t *testing.T) {
		got, err := resolveExecCredentials(identity("3456:4567"), lookups)
		require.NoError(t, err)
		require.Equal(t, uint32(3456), got.uid)
		require.Equal(t, uint32(4567), got.gid)
		require.NotNil(t, got.groups)
		require.Empty(t, got.groups)
		require.Nil(t, got.account)
	})

	t.Run("named account with named group", func(t *testing.T) {
		got, err := resolveExecCredentials(identity("alice:staff"), lookups)
		require.NoError(t, err)
		require.Equal(t, uint32(1000), got.uid)
		require.Equal(t, uint32(3000), got.gid)
		require.Empty(t, got.groups)
	})

	t.Run("unknown UID without group fails clearly", func(t *testing.T) {
		_, err := resolveExecCredentials(identity("3456"), lookups)
		require.ErrorContains(t, err, "specify UID:GID")
	})
}

func TestPrepareExecIdentityPreservesCommandAttributes(t *testing.T) {
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	before := *cmd.SysProcAttr
	lookups := execIdentityLookups{
		userByID: func(string) (*user.User, error) { return nil, errors.New("no account") },
	}
	cmd.Env = []string{"PATH=/bin", "HOME=/root", "USER=root", "LOGNAME=root"}
	handled, err := prepareExecIdentityWithLookups(cmd, "1234:2345", lookups)
	require.NoError(t, err)
	require.True(t, handled)
	require.True(t, cmd.SysProcAttr.Setpgid == before.Setpgid)
	require.Equal(t, uint32(1234), cmd.SysProcAttr.Credential.Uid)
	require.Equal(t, uint32(2345), cmd.SysProcAttr.Credential.Gid)
	require.NotNil(t, cmd.SysProcAttr.Credential.Groups)
	env := environmentMap(cmd.Environ())
	require.Equal(t, "/", env["HOME"])
	require.Equal(t, "1234", env["USER"])
	require.Equal(t, "1234", env["LOGNAME"])
	require.Equal(t, "/bin", env["PATH"])
}

func TestPrepareExecIdentitySkipsCredentialForCurrentProcessIdentity(t *testing.T) {
	uid := strconv.Itoa(os.Geteuid())
	gid := strconv.Itoa(os.Getegid())
	currentGroups, err := os.Getgroups()
	require.NoError(t, err)
	groupIDs := make([]string, len(currentGroups))
	for i, group := range currentGroups {
		groupIDs[i] = strconv.Itoa(group)
	}
	account := &user.User{
		Username: "current",
		Uid:      uid,
		Gid:      gid,
		HomeDir:  "/tmp",
	}
	lookups := execIdentityLookups{
		userByID: func(id string) (*user.User, error) {
			require.Equal(t, uid, id)
			return account, nil
		},
		groups: func(got *user.User) ([]string, error) {
			require.Same(t, account, got)
			return groupIDs, nil
		},
	}
	cmd := exec.Command("true")
	attrs := &syscall.SysProcAttr{Setpgid: true}
	cmd.SysProcAttr = attrs

	handled, err := prepareExecIdentityWithLookups(cmd, uid, lookups)
	require.NoError(t, err)
	require.True(t, handled)
	require.Same(t, attrs, cmd.SysProcAttr)
	require.Nil(t, cmd.SysProcAttr.Credential)
	require.Equal(t, "/tmp", cmd.Dir)
}

func TestSameEffectiveGroupIDsIgnoresOrderDuplicatesAndPrimaryRepresentation(t *testing.T) {
	require.True(t, sameEffectiveGroupIDs(200, []uint32{300, 200, 200}, 200, []int{200, 300}))
	require.True(t, sameEffectiveGroupIDs(200, []uint32{300}, 200, []int{300}))
	require.True(t, sameEffectiveGroupIDs(200, nil, 200, nil))
	require.False(t, sameEffectiveGroupIDs(200, []uint32{300}, 200, nil))
	require.False(t, sameEffectiveGroupIDs(200, nil, 200, []int{300}))
	require.False(t, sameEffectiveGroupIDs(200, nil, 200, []int{201}))
	require.False(t, sameEffectiveGroupIDs(201, nil, 201, []int{-1}))
}

func TestPrepareExecIdentityLeavesNamedUserToExistingPath(t *testing.T) {
	cmd := exec.Command("true")
	attrs := &syscall.SysProcAttr{Setpgid: true}
	cmd.SysProcAttr = attrs
	handled, err := prepareExecIdentityWithLookups(cmd, "vscode", execIdentityLookups{})
	require.NoError(t, err)
	require.False(t, handled)
	require.Same(t, attrs, cmd.SysProcAttr)
}

func environmentMap(env []string) map[string]string {
	values := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	return values
}

func execIdentityTestLookups() (*user.User, execIdentityLookups) {
	account := &user.User{Username: "alice", Uid: "1000", Gid: "2000", HomeDir: "/home/alice"}
	lookups := execIdentityLookups{
		userByName: func(name string) (*user.User, error) {
			if name == "alice" {
				return account, nil
			}
			return nil, errors.New("unknown user")
		},
		userByID: func(id string) (*user.User, error) {
			if id == "1000" {
				return account, nil
			}
			return nil, errors.New("unknown UID")
		},
		groupByName: func(name string) (*user.Group, error) {
			if name == "staff" {
				return &user.Group{Name: name, Gid: "3000"}, nil
			}
			return nil, errors.New("unknown group")
		},
		groups: func(*user.User) ([]string, error) { return []string{"2001", "2002"}, nil },
	}
	return account, lookups
}

func TestNumericSSHIdentityUsesResolvedAccountHome(t *testing.T) {
	account, err := user.Current()
	require.NoError(t, err)
	for _, identity := range []string{account.Uid, account.Uid + ":" + account.Gid} {
		t.Run(identity, func(t *testing.T) {
			srv := &server{currentUser: "separate-server-account", shell: []string{execTestShell}}
			session := sessionEnvironmentStub{user: identity}
			cmd, err := srv.getCommandWithSecretEnvironment(session, false, nil)
			require.NoError(t, err)
			require.Equal(t, account.HomeDir, cmd.Dir)
			srv.workdir = t.TempDir()
			cmd, err = srv.getCommandWithSecretEnvironment(session, false, nil)
			require.NoError(t, err)
			require.Equal(t, srv.workdir, cmd.Dir)
		})
	}
}
