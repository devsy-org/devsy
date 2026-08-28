package ts

import (
	"testing"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/key"
)

func TestSelectRunnerPeer(t *testing.T) {
	cases := []struct {
		name      string
		peers     map[key.NodePublic]*ipnstate.PeerStatus
		wantHost  string
		wantFound bool
	}{
		{
			name:      "no peers",
			peers:     map[key.NodePublic]*ipnstate.PeerStatus{},
			wantFound: false,
		},
		{
			name: "no runner peer",
			peers: map[key.NodePublic]*ipnstate.PeerStatus{
				{}: {HostName: "some-client"},
			},
			wantFound: false,
		},
		{
			name: "nil peer entry is skipped",
			peers: map[key.NodePublic]*ipnstate.PeerStatus{
				{}: nil,
			},
			wantFound: false,
		},
		{
			name: "runner peer found",
			peers: map[key.NodePublic]*ipnstate.PeerStatus{
				{}: {HostName: "acme-project-abc123-runner"},
			},
			wantHost:  "acme-project-abc123-runner",
			wantFound: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, found := selectRunnerPeer(tc.peers)
			if found != tc.wantFound {
				t.Fatalf("selectRunnerPeer() found = %v, want %v", found, tc.wantFound)
			}
			if found && host != tc.wantHost {
				t.Errorf("selectRunnerPeer() host = %q, want %q", host, tc.wantHost)
			}
		})
	}
}

func TestRunnerClientURL(t *testing.T) {
	rc := &runnerClient{projectName: "acme", workspaceName: "ws1"}

	got := rc.url("acme-ws1-runner", "workspace-git-credentials")
	want := "http://acme-ws1-runner.ts.devsy/devsy/acme/ws1/workspace-git-credentials"
	if got != want {
		t.Errorf("url() = %q, want %q", got, want)
	}
}
