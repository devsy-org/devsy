package channel

import (
	"net/netip"
	"testing"
	"unicode"

	"github.com/devsy-org/devsy/pkg/pool/egress"
	"github.com/stretchr/testify/require"
)

// Whatever Decode accepts must be a value the proxy could have produced, and
// must encode back to a line that decodes to the same value.
func FuzzDecode(f *testing.F) {
	for _, seed := range []string{
		`{"t":"ready"}`,
		`{"t":"deny","host":"a.example.com","port":443,"reason":"pool_network","peer":"172.17.0.9"}`,
		`{"t":"deny","redacted":true,"port":0,"reason":"bad_request"}`,
		`{"t":"allow","host":"203.0.113.7","port":443,"rule":3}`,
		`{"t":"allow","host":"::ffff:10.0.0.1","port":443}`,
		`{"t":"allow","host":"a.example.com","port":443,"session":"x"}`,
		`{"t":"deny","host":"a.example.com","port":443,"reason":"x"}`,
		`{"t":"ready"}{"t":"ready"}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		ev, err := Decode(raw)
		if err != nil {
			require.Equal(t, "invalid egress event", err.Error())
			return
		}
		switch ev.Kind {
		case KindDeny:
			checkTarget(t, ev.Denial.Target)
			require.True(t, ev.Denial.Reason.Valid())
			checkPeer(t, ev.Denial.Peer)
			again, err := EncodeDenial(ev.Denial)
			require.NoError(t, err)
			back, err := Decode(again)
			require.NoError(t, err)
			require.Equal(t, ev, back)
		case KindAllow:
			checkTarget(t, ev.Allow.Target)
			require.GreaterOrEqual(t, ev.Allow.Port, 1)
			again, err := EncodeAllow(ev.Allow)
			require.NoError(t, err)
			back, err := Decode(again)
			require.NoError(t, err)
			require.Equal(t, ev, back)
		case KindReady:
		default:
			t.Fatalf("unknown kind %d", ev.Kind)
		}
	})
}

func checkTarget(t *testing.T, target egress.Target) {
	t.Helper()
	require.LessOrEqual(t, target.Port, maxPort)
	require.GreaterOrEqual(t, target.Port, 0)
	if target.AddressRedacted {
		require.Empty(t, target.Host)
		return
	}
	require.True(t, validHost(target.Host))
	for _, r := range target.Host {
		require.False(t, unicode.IsControl(r) || unicode.IsSpace(r) || r > unicode.MaxASCII)
	}
}

func checkPeer(t *testing.T, peer netip.Addr) {
	t.Helper()
	if !peer.IsValid() {
		return
	}
	require.Empty(t, peer.Zone())
	require.False(t, peer.Is4In6())
}
