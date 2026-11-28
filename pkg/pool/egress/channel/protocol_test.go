package channel

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/pool/egress"
	"github.com/stretchr/testify/suite"
)

const (
	hostA   = "a.example.com"
	peerV4  = "172.17.0.9"
	peerV6  = "2001:db8::1"
	okHost  = `"host":"a.example.com"`
	denyHdr = `{"t":"deny",` + okHost + `,"port":443,"reason":"not_allowed"`
)

type ProtocolSuite struct {
	suite.Suite
}

func TestProtocolSuite(t *testing.T) {
	suite.Run(t, new(ProtocolSuite))
}

func (s *ProtocolSuite) TestGoldenLines() {
	deny, err := EncodeDenial(egress.Denial{
		Target: egress.Target{Host: hostA, Port: 443},
		Reason: egress.PoolNetwork,
		Peer:   netip.MustParseAddr(peerV4),
	})
	s.Require().NoError(err)
	s.Equal(
		`{"t":"deny","host":"a.example.com","port":443,"reason":"pool_network","peer":"172.17.0.9"}`+"\n",
		string(deny),
	)

	redacted, err := EncodeDenial(egress.Denial{
		Target: egress.Target{Port: 443, AddressRedacted: true},
		Reason: egress.BadRequest,
	})
	s.Require().NoError(err)
	s.Equal(`{"t":"deny","redacted":true,"port":443,"reason":"bad_request"}`+"\n", string(redacted))

	allow, err := EncodeAllow(egress.Allow{
		Target: egress.Target{Host: hostA, Port: 443},
		Rule:   egress.RuleAt(2),
	})
	s.Require().NoError(err)
	s.Equal(`{"t":"allow","host":"a.example.com","port":443,"rule":2}`+"\n", string(allow))

	s.Equal(`{"t":"ready"}`+"\n", string(encodeReady()))
}

func (s *ProtocolSuite) TestEveryLineRoundTrips() {
	for _, d := range []egress.Denial{
		{Target: egress.Target{Host: hostA, Port: 443}, Reason: egress.NotAllowed},
		{Target: egress.Target{Host: "203.0.113.7", Port: 8443}, Reason: egress.IPLiteral},
		{Target: egress.Target{Host: "::ffff:10.0.0.1", Port: 443}, Reason: egress.IPLiteral},
		{Target: egress.Target{Host: peerV6, Port: 1}, Reason: egress.IPLiteral},
		{Target: egress.Target{Port: 0, AddressRedacted: true}, Reason: egress.BadRequest},
		{
			Target: egress.Target{Host: hostA, Port: 443}, Reason: egress.PoolNetwork,
			Peer: netip.MustParseAddr(peerV6),
		},
	} {
		raw, err := EncodeDenial(d)
		s.Require().NoError(err)
		ev, err := Decode(raw)
		s.Require().NoError(err, string(raw))
		s.Equal(KindDeny, ev.Kind)
		s.Equal(d, ev.Denial, string(raw))
	}
	for _, a := range []egress.Allow{
		{Target: egress.Target{Host: hostA, Port: 443}},
		{Target: egress.Target{Host: hostA, Port: 65535}, Rule: egress.RuleAt(0)},
		{Target: egress.Target{Port: 443, AddressRedacted: true}, Rule: egress.RuleAt(MaxRule)},
	} {
		raw, err := EncodeAllow(a)
		s.Require().NoError(err)
		ev, err := Decode(raw)
		s.Require().NoError(err, string(raw))
		s.Equal(KindAllow, ev.Kind)
		s.Equal(a, ev.Allow, string(raw))
	}
	ev, err := Decode(encodeReady())
	s.Require().NoError(err)
	s.Equal(KindReady, ev.Kind)
}

func (s *ProtocolSuite) TestDecodeAcceptsALineWithoutItsNewline() {
	_, err := Decode([]byte(`{"t":"ready"}`))
	s.NoError(err)
}

func (s *ProtocolSuite) TestDecodeRefusesAnythingElse() {
	long := strings.Repeat("a", MaxLine)
	for name, raw := range map[string]string{
		"empty":                "",
		"only a newline":       "\n",
		"empty object":         `{}`,
		"unknown kind":         `{"t":"nope"}`,
		"unknown field":        `{"t":"ready","extra":1}`,
		"session field":        `{"t":"allow",` + okHost + `,"port":443,"session":"p-other-aaaaaaaaaa"}`,
		"not json":             `deny a.example.com`,
		"array":                `[]`,
		"null":                 `null`,
		"string port":          `{"t":"allow",` + okHost + `,"port":"443"}`,
		"trailing object":      `{"t":"ready"}{"t":"ready"}`,
		"duplicate key":        `{"t":"ready","t":"ready"}`,
		"duplicate host":       `{"t":"allow","host":"a.example.com","host":"b.example.com","port":443}`,
		"cased key":            `{"T":"ready"}`,
		"cased field":          `{"t":"allow","HOST":"a.example.com","port":443}`,
		"reordered fields":     `{"t":"allow","port":443,"host":"a.example.com"}`,
		"spaced":               `{"t": "ready"}`,
		"escaped letter":       `{"t":"rea\u0064y"}`,
		"explicit zero rule":   `{"t":"allow","host":"a.example.com","port":443,"rule":null}`,
		"false flag":           `{"t":"allow","host":"a.example.com","port":443,"redacted":false}`,
		"trailing text":        `{"t":"ready"} x`,
		"two lines":            "{\"t\":\"ready\"}\n{\"t\":\"ready\"}",
		"ready with port":      `{"t":"ready","port":443}`,
		"unknown reason":       `{"t":"deny",` + okHost + `,"port":443,"reason":"made_up"}`,
		"no reason":            `{"t":"deny",` + okHost + `,"port":443}`,
		"upper case host":      `{"t":"allow","host":"A.example.com","port":443}`,
		"trailing dot":         `{"t":"allow","host":"a.example.com.","port":443}`,
		"space in host":        `{"t":"allow","host":"a b.com","port":443}`,
		"underscore host":      `{"t":"allow","host":"a_b.example.com","port":443}`,
		"internal host":        `{"t":"allow","host":"db.internal","port":443}`,
		"no dot host":          `{"t":"allow","host":"localhost","port":443}`,
		"unicode host":         `{"t":"allow","host":"a.exämple.com","port":443}`,
		"zoned literal":        `{"t":"allow","host":"fe80::1%eth0","port":443}`,
		"upper case literal":   `{"t":"allow","host":"2001:DB8::1","port":443}`,
		"padded literal":       `{"t":"allow","host":"2001:0db8::1","port":443}`,
		"redacted and host":    `{"t":"allow","redacted":true,` + okHost + `,"port":443}`,
		"no host":              `{"t":"allow","port":443}`,
		"port too big":         `{"t":"allow",` + okHost + `,"port":65536}`,
		"negative port":        `{"t":"deny",` + okHost + `,"port":-1,"reason":"not_allowed"}`,
		"allow port zero":      `{"t":"allow",` + okHost + `}`,
		"allow with reason":    `{"t":"allow",` + okHost + `,"port":443,"reason":"not_allowed"}`,
		"allow with peer":      `{"t":"allow",` + okHost + `,"port":443,"peer":"` + peerV4 + `"}`,
		"deny with rule":       denyHdr + `,"rule":1}`,
		"negative rule":        `{"t":"allow",` + okHost + `,"port":443,"rule":-1}`,
		"rule too big":         `{"t":"allow",` + okHost + `,"port":443,"rule":65536}`,
		"zoned peer":           denyHdr + `,"peer":"fe80::1%eth0"}`,
		"mapped peer":          denyHdr + `,"peer":"::ffff:1.2.3.4"}`,
		"upper case peer":      denyHdr + `,"peer":"2001:DB8::1"}`,
		"peer not an address":  denyHdr + `,"peer":"evil"}`,
		"peer with text":       denyHdr + `,"peer":"1.2.3.4 x"}`,
		"line too long":        `{"t":"deny","host":"` + long + `","port":443,"reason":"not_allowed"}`,
		"deny without port ok": `{"t":"deny","redacted":true,"port":443,"reason":"bad_request","host":"x"}`,
	} {
		_, err := Decode([]byte(raw))
		s.Require().Error(err, name)
		s.Equal("invalid egress event", err.Error(), name)
	}
}

func (s *ProtocolSuite) TestTheWorstCaseLineFits() {
	host := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." +
		strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	s.Len(host, 253)
	raw, err := EncodeDenial(egress.Denial{
		Target: egress.Target{Host: host, Port: 65535},
		Reason: egress.ResolveFailed,
		Peer:   netip.MustParseAddr("2001:db8:1111:2222:3333:4444:5555:6666"),
	})
	s.Require().NoError(err)
	s.LessOrEqual(len(raw), MaxLine)
	_, err = Decode(raw)
	s.NoError(err)
}

func (s *ProtocolSuite) TestEncodeKeepsOnlyWhatThePoolWouldAccept() {
	raw, err := EncodeDenial(egress.Denial{
		Target: egress.Target{Host: "A b.COM", Port: 443},
		Reason: egress.NotAllowed,
		Peer:   netip.MustParseAddr("fe80::1%eth0"),
	})
	s.Require().NoError(err)
	s.Equal(`{"t":"deny","redacted":true,"port":443,"reason":"not_allowed"}`+"\n", string(raw))

	raw, err = EncodeDenial(egress.Denial{
		Target: egress.Target{Host: hostA, Port: 443},
		Reason: egress.NotAllowed,
		Peer:   netip.MustParseAddr("::ffff:1.2.3.4"),
	})
	s.Require().NoError(err)
	s.NotContains(string(raw), "peer")

	raw, err = EncodeAllow(egress.Allow{
		Target: egress.Target{Host: hostA, Port: 443}, Rule: egress.RuleAt(MaxRule + 1),
	})
	s.Require().NoError(err)
	s.NotContains(string(raw), "rule")

	raw, err = EncodeDenial(egress.Denial{
		Target: egress.Target{Host: "", Port: 443}, Reason: egress.BadRequest,
	})
	s.Require().NoError(err)
	s.Contains(string(raw), `"redacted":true`)
}

func (s *ProtocolSuite) TestEncodeRefusesWhatCannotBeRecorded() {
	for name, d := range map[string]egress.Denial{
		"unknown reason": {Target: egress.Target{Host: hostA, Port: 443}, Reason: "made_up"},
		"no reason":      {Target: egress.Target{Host: hostA, Port: 443}},
		"port too big":   {Target: egress.Target{Host: hostA, Port: 70000}, Reason: egress.NotAllowed},
		"negative port":  {Target: egress.Target{Host: hostA, Port: -1}, Reason: egress.NotAllowed},
	} {
		_, err := EncodeDenial(d)
		s.Error(err, name)
	}
	for _, port := range []int{0, -1, 65536} {
		_, err := EncodeAllow(egress.Allow{Target: egress.Target{Host: hostA, Port: port}})
		s.Error(err, port)
	}
}
