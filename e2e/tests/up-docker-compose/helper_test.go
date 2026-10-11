//go:build linux || darwin || unix

package up

import "testing"

func TestExpectedUIDMapping(t *testing.T) {
	const (
		linux  = "linux"
		darwin = "darwin"
	)
	tests := []struct {
		name                   string
		goos                   string
		hostUID, hostGID       int
		defaultUID, defaultGID int
		wantUID, wantGID       int
	}{
		{"linux root www-data", linux, 0, 0, 33, 33, 33, 33},
		{"linux root vscode", linux, 0, 0, 1001, 1001, 1001, 1001},
		{"linux root nonzero GID www-data", linux, 0, 2002, 33, 33, 33, 33},
		{"linux root nonzero GID vscode", linux, 0, 2002, 1001, 1001, 1001, 1001},
		{"linux 501 www-data", linux, 501, 20, 33, 33, 501, 20},
		{"linux 501 vscode", linux, 501, 20, 1001, 1001, 501, 20},
		{"linux distinct IDs www-data", linux, 2000, 2002, 33, 33, 2000, 2002},
		{"linux distinct IDs vscode", linux, 2000, 2002, 1001, 1001, 2000, 2002},
		{"linux non-root zero GID www-data", linux, 2000, 0, 33, 33, 2000, 0},
		{"linux non-root zero GID vscode", linux, 2000, 0, 1001, 1001, 2000, 0},
		{"darwin user www-data", darwin, 501, 20, 33, 33, 33, 33},
		{"darwin user vscode", darwin, 501, 20, 1001, 1001, 1001, 1001},
		{"darwin root www-data", darwin, 0, 0, 33, 33, 33, 33},
		{"darwin root vscode", darwin, 0, 0, 1001, 1001, 1001, 1001},
		{"freebsd user www-data", "freebsd", 501, 20, 33, 33, 33, 33},
		{"freebsd user vscode", "freebsd", 501, 20, 1001, 1001, 1001, 1001},
		{"linux matching defaults www-data", linux, 33, 33, 33, 33, 33, 33},
		{"linux matching defaults vscode", linux, 1001, 1001, 1001, 1001, 1001, 1001},
		{"linux UID mismatch only www-data", linux, 2000, 33, 33, 33, 2000, 33},
		{"linux UID mismatch only vscode", linux, 2000, 1001, 1001, 1001, 2000, 1001},
		{"linux GID mismatch only www-data", linux, 33, 2002, 33, 33, 33, 2002},
		{"linux GID mismatch only vscode", linux, 1001, 2002, 1001, 1001, 1001, 2002},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			uid, gid := expectedUIDMapping(
				tc.goos,
				userIDs{uid: tc.hostUID, gid: tc.hostGID},
				userIDs{uid: tc.defaultUID, gid: tc.defaultGID},
			)
			if uid != tc.wantUID || gid != tc.wantGID {
				t.Fatalf("got %d:%d want %d:%d", uid, gid, tc.wantUID, tc.wantGID)
			}
		})
	}
}
