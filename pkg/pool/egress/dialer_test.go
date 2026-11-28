package egress

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
	"github.com/stretchr/testify/suite"
)

const (
	checkedHost = "a.example.com"
	publicAddr  = "8.8.8.8"
	loopback4   = "127.0.0.1"
	mappedLoop  = "::ffff:127.0.0.1"
)

type fakeResolver struct {
	answers  []netip.Addr
	err      error
	names    []string
	deadline time.Duration
}

func (f *fakeResolver) LookupNetIP(
	ctx context.Context,
	network, host string,
) ([]netip.Addr, error) {
	f.names = append(f.names, network+" "+host)
	if dl, ok := ctx.Deadline(); ok {
		f.deadline = time.Until(dl)
	}
	return f.answers, f.err
}

// fakeNet stands in for the socket layer. Like the real one it runs the
// verify hook with the address it is about to connect.
type fakeNet struct {
	targets []netip.AddrPort
	fail    map[netip.AddrPort]error
	probe   string
	// probeFirst applies probe to the first connect only.
	probeFirst bool
}

func (f *fakeNet) connect(
	_ context.Context, target netip.AddrPort, verify func(string) error,
) (net.Conn, error) {
	f.targets = append(f.targets, target)
	address := target.String()
	if f.probe != "" && (!f.probeFirst || len(f.targets) == 1) {
		address = f.probe
	}
	if err := verify(address); err != nil {
		return nil, err
	}
	if err := f.fail[target]; err != nil {
		return nil, err
	}
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

type DialerSuite struct {
	suite.Suite
}

func TestDialerSuite(t *testing.T) {
	suite.Run(t, new(DialerSuite))
}

func newTestDialer(
	tb testing.TB, answers []netip.Addr, rules ...poolconfig.AllowRule,
) (*Dialer, *fakeResolver, *fakeNet) {
	res := &fakeResolver{answers: answers}
	sock := &fakeNet{fail: map[netip.AddrPort]error{}}
	d := &Dialer{
		policy:   newPolicy(tb, rules, "172.17.0.0/16"),
		resolver: res,
		connect:  sock.connect,
	}
	return d, res, sock
}

func reasonOf(tb testing.TB, err error) Reason {
	tb.Helper()
	refused, ok := errors.AsType[*DeniedError](err)
	if !ok {
		tb.Fatalf("not a denial: %v", err)
	}
	return refused.Reason
}

func (s *DialerSuite) TestADeniedNameCausesNoLookupAndNoConnect() {
	d, res, sock := newTestDialer(s.T(), addrs(publicAddr), host(hostGitHub))
	for _, h := range []string{"evil.example.org", "localhost", "a b.com", ""} {
		_, err := d.Dial(context.Background(), h, 443)
		s.Require().Error(err, h)
		s.Contains([]Reason{NotAllowed, BadRequest}, reasonOf(s.T(), err), h)
	}
	_, err := d.Dial(context.Background(), hostGitHub, 80)
	s.Equal(NotAllowed, reasonOf(s.T(), err))
	s.Empty(res.names)
	s.Empty(sock.targets)
}

func (s *DialerSuite) TestTheNameIsResolvedOnceAsAQualifiedName() {
	d, res, sock := newTestDialer(s.T(), addrs(publicAddr), host(hostWild))
	conn, err := d.Dial(context.Background(), "A.Example.COM.", 443)
	s.Require().NoError(err)
	s.Require().NoError(conn.Close())
	s.Equal([]string{"ip a.example.com."}, res.names)
	s.LessOrEqual(res.deadline, resolveTimeout)
	s.Positive(res.deadline)
	s.Len(sock.targets, 1)
}

func (s *DialerSuite) TestALookupErrorDenies() {
	d, res, sock := newTestDialer(s.T(), nil, host(hostWild))
	res.err = errors.New("no such host")
	_, err := d.Dial(context.Background(), checkedHost, 443)
	s.Equal(ResolveFailed, reasonOf(s.T(), err))

	res.err = nil
	_, err = d.Dial(context.Background(), checkedHost, 443)
	s.Equal(ResolveFailed, reasonOf(s.T(), err), "no answers")
	s.Empty(sock.targets)
}

func (s *DialerSuite) TestOneBadAnswerDeniesTheWholeRequest() {
	for _, bad := range []string{loopback4, metadataIP, "172.17.0.5", zonedLL, mappedLoop, privateIP} {
		d, _, sock := newTestDialer(s.T(), addrs(publicAddr, bad, "8.8.4.4"), host(hostWild))
		_, err := d.Dial(context.Background(), checkedHost, 443)
		s.Require().Error(err, bad)
		s.Contains([]Reason{ReservedRange, PoolNetwork, PrivateRange}, reasonOf(s.T(), err), bad)
		s.Empty(sock.targets, bad)
	}
}

func (s *DialerSuite) TestAPrivateAnswerNeedsACoveringCidr() {
	d, _, sock := newTestDialer(s.T(), addrs(privateIP), host(hostWild), cidr(cidrPrivate))
	conn, err := d.Dial(context.Background(), checkedHost, 443)
	s.Require().NoError(err)
	s.Require().NoError(conn.Close())
	s.Equal(netip.MustParseAddrPort(privateIP+":443"), sock.targets[0])
}

func (s *DialerSuite) TestItDialsOnlyTheUnmappedCheckedAddresses() {
	answers := addrs("::ffff:8.8.8.8", "2606:4700::1111", publicAddr)
	d, _, sock := newTestDialer(s.T(), answers, host(hostWild))
	sock.fail[netip.MustParseAddrPort("8.8.8.8:443")] = errors.New("refused")
	sock.fail[netip.MustParseAddrPort("[2606:4700::1111]:443")] = errors.New("refused")
	_, err := d.Dial(context.Background(), checkedHost, 443)
	s.Require().Error(err)
	_, isDenial := errors.AsType[*DeniedError](err)
	s.False(isDenial)
	want := []string{"8.8.8.8:443", "[2606:4700::1111]:443", "8.8.8.8:443"}
	s.Require().Len(sock.targets, len(want))
	for i, target := range sock.targets {
		s.Equal(want[i], target.String())
		s.False(target.Addr().Is4In6(), target.String())
	}
}

func (s *DialerSuite) TestItStopsAtTheFirstSuccess() {
	d, _, sock := newTestDialer(s.T(), addrs("8.8.4.4", publicAddr), host(hostWild))
	sock.fail[netip.MustParseAddrPort("8.8.4.4:443")] = errors.New("refused")
	conn, err := d.Dial(context.Background(), checkedHost, 443)
	s.Require().NoError(err)
	s.Require().NoError(conn.Close())
	s.Len(sock.targets, 2)
}

func (s *DialerSuite) TestTheConnectStepRefusesAnyOtherAddress() {
	for probe, want := range map[string]Reason{
		metadataIP + ":443":     ReservedRange,
		"[" + zonedLL + "]:443": ReservedRange,
		"[::1]:443":             ReservedRange,
		"172.17.0.5:443":        PoolNetwork,
		"8.8.4.4:443":           NotAllowed,
		publicAddr + ":80":      NotAllowed,
		"[::ffff:8.8.4.4]:443":  NotAllowed,
		"not an address":        BadRequest,
		publicAddr:              BadRequest,
		"[::ffff:10.0.0.1]:443": PrivateRange,
	} {
		d, _, sock := newTestDialer(s.T(), addrs(publicAddr), host(hostWild))
		sock.probe = probe
		_, err := d.Dial(context.Background(), checkedHost, 443)
		s.Require().Error(err, probe)
		s.Equal(want, reasonOf(s.T(), err), probe)
	}
	d, _, sock := newTestDialer(s.T(), addrs(publicAddr), host(hostWild))
	sock.probe = "[::ffff:8.8.8.8]:443"
	conn, err := d.Dial(context.Background(), checkedHost, 443)
	s.Require().NoError(err)
	s.Require().NoError(conn.Close())
}

func (s *DialerSuite) TestARefusedSocketAddressStopsTheLoop() {
	d, _, sock := newTestDialer(s.T(), addrs("8.8.4.4", publicAddr), host(hostWild))
	sock.probe, sock.probeFirst = metadataIP+":443", true
	_, err := d.Dial(context.Background(), checkedHost, 443)
	s.Equal(ReservedRange, reasonOf(s.T(), err))
	s.Len(sock.targets, 1)
}

func (s *DialerSuite) TestTheWholeDialHasATimeBudget() {
	d, _, sock := newTestDialer(s.T(), addrs("8.8.4.4", publicAddr, "8.8.8.9"), host(hostWild))
	d.budget = 50 * time.Millisecond
	d.connect = func(ctx context.Context, t netip.AddrPort, _ func(string) error) (net.Conn, error) {
		sock.targets = append(sock.targets, t)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	start := time.Now()
	_, err := d.Dial(context.Background(), checkedHost, 443)
	s.Require().Error(err)
	s.Less(time.Since(start), 5*time.Second)
	s.Len(sock.targets, 1)
	s.ErrorIs(err, context.DeadlineExceeded)
}

func (s *DialerSuite) TestNoAddressesIsRefused() {
	d, _, sock := newTestDialer(s.T(), nil, host(hostWild))
	_, err := d.dialChecked(context.Background(), nil, 443,
		func(netip.Addr) Decision { return allowed })
	s.Equal(ResolveFailed, reasonOf(s.T(), err))
	s.Empty(sock.targets)
}

func (s *DialerSuite) TestTheConnectStepRefusesABadPortToo() {
	d, _, sock := newTestDialer(s.T(), nil, host(hostWild))
	for _, port := range []int{0, -1, 65536} {
		_, err := d.dialChecked(context.Background(), addrs(publicAddr), port,
			func(netip.Addr) Decision { return allowed })
		s.Equal(BadRequest, reasonOf(s.T(), err), port)
	}
	s.Empty(sock.targets)
}

func (s *DialerSuite) TestALiteralNeedsACidrRule() {
	d, res, sock := newTestDialer(s.T(), nil, host(hostWild))
	_, err := d.Dial(context.Background(), publicAddr, 443)
	s.Equal(IPLiteral, reasonOf(s.T(), err))
	s.Empty(sock.targets)
	s.Empty(res.names)

	d, res, sock = newTestDialer(s.T(), nil, host(hostWild), cidr("8.8.8.0/24"))
	conn, err := d.Dial(context.Background(), publicAddr, 443)
	s.Require().NoError(err)
	s.Require().NoError(conn.Close())
	s.Empty(res.names)
	s.Equal(publicAddr+":443", sock.targets[0].String())

	conn, err = d.Dial(context.Background(), "::ffff:8.8.8.8", 443)
	s.Require().NoError(err)
	s.Require().NoError(conn.Close())
	s.Equal(publicAddr+":443", sock.targets[1].String())
}

func (s *DialerSuite) TestALiteralInAReservedRangeIsRefusedWhateverTheRules() {
	d, _, sock := newTestDialer(s.T(), nil, cidr("169.254.0.0/16"), cidr("127.0.0.0/8"))
	for _, h := range []string{metadataIP, loopback4, loopback6, zonedLL, mappedLoop} {
		_, err := d.Dial(context.Background(), h, 443)
		s.Equal(ReservedRange, reasonOf(s.T(), err), h)
	}
	s.Empty(sock.targets)
}

func (s *DialerSuite) TestAPortOutsideTheRangeIsRefused() {
	d, _, sock := newTestDialer(s.T(), addrs(publicAddr), host(hostWild))
	for _, port := range []int{0, -1, 65536} {
		_, err := d.Dial(context.Background(), checkedHost, port)
		s.Equal(BadRequest, reasonOf(s.T(), err), port)
	}
	s.Empty(sock.targets)
}

func (s *DialerSuite) TestACancelledContextStopsTheAddressLoop() {
	d, _, sock := newTestDialer(s.T(), addrs("8.8.4.4", publicAddr), host(hostWild))
	ctx, cancel := context.WithCancel(context.Background())
	d.connect = func(_ context.Context, t netip.AddrPort, _ func(string) error) (net.Conn, error) {
		sock.targets = append(sock.targets, t)
		cancel()
		return nil, errors.New("refused")
	}
	_, err := d.Dial(ctx, checkedHost, 443)
	s.Require().Error(err)
	s.Len(sock.targets, 1)
}

func loopbackListener(tb testing.TB) *net.TCPListener {
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = ln.Close() })
	return ln
}

func (s *DialerSuite) TestTheRealSocketRunsTheVerifyHookBeforeConnecting() {
	ln := loopbackListener(s.T())
	target := ln.Addr().(*net.TCPAddr).AddrPort()

	var seen string
	conn, err := connectTCP(context.Background(), target, func(address string) error {
		seen = address
		return nil
	})
	s.Require().NoError(err)
	s.Require().NoError(conn.Close())
	s.Equal(target.String(), seen)

	s.Require().NoError(ln.SetDeadline(time.Now().Add(time.Second)))
	accepted, err := ln.Accept()
	s.Require().NoError(err)
	s.Require().NoError(accepted.Close())

	s.Require().NoError(ln.SetDeadline(time.Now().Add(200 * time.Millisecond)))
	_, err = connectTCP(context.Background(), target, func(string) error {
		return &DeniedError{Reason: NotAllowed}
	})
	s.Equal(NotAllowed, reasonOf(s.T(), err))
	_, err = ln.Accept()
	var netErr net.Error
	s.Require().ErrorAs(err, &netErr)
	s.True(netErr.Timeout(), "the refused connect must not reach the listener")
}

func (s *DialerSuite) TestADefaultDialerRefusesWithoutTouchingTheNetwork() {
	d := NewDialer(newPolicy(s.T(), []poolconfig.AllowRule{host(hostWild), cidr("127.0.0.0/8")}))
	for _, h := range []string{loopback4, loopback6, "localhost", "x.internal"} {
		_, err := d.Dial(context.Background(), h, 443)
		s.Require().Error(err, h)
		s.NotEmpty(reasonOf(s.T(), err), h)
	}
}
