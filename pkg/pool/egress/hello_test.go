package egress

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"golang.org/x/crypto/cryptobyte"
)

const (
	apiHost    = "api.example.org"
	sniHost    = "github.com"
	extGroups  = 10
	extECH     = 0xfe0d
	extESNI    = 0xffce
	extOuter   = 0xfd00
	extECHDraf = 0xfe08
)

type HelloSuite struct {
	suite.Suite
}

func TestHelloSuite(t *testing.T) {
	suite.Run(t, new(HelloSuite))
}

type sniEntry struct {
	kind uint8
	name string
}

func ext(typ uint16, data []byte) []byte {
	b := cryptobyte.NewBuilder(nil)
	b.AddUint16(typ)
	b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(data) })
	return b.BytesOrPanic()
}

func sniData(entries ...sniEntry) []byte {
	b := cryptobyte.NewBuilder(nil)
	b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
		for _, e := range entries {
			b.AddUint8(e.kind)
			b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes([]byte(e.name)) })
		}
	})
	return b.BytesOrPanic()
}

func sniExt(names ...string) []byte {
	entries := make([]sniEntry, len(names))
	for i, n := range names {
		entries[i] = sniEntry{name: n}
	}
	return ext(extServerName, sniData(entries...))
}

// helloFields describes a ClientHello body. The zero value is a valid hello
// without extensions.
type helloFields struct {
	msgType     uint8
	sessionLen  int
	ciphers     []byte
	compression []byte
	exts        [][]byte
	rawExts     []byte
	trailing    []byte
}

func goodFields(exts ...[]byte) helloFields {
	return helloFields{
		msgType:     handshakeClientHello,
		sessionLen:  32,
		ciphers:     []byte{0x13, 0x01},
		compression: []byte{0},
		exts:        exts,
	}
}

func (f helloFields) body() []byte {
	b := cryptobyte.NewBuilder(nil)
	b.AddUint8(f.msgType)
	b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
		b.AddUint16(0x0303)
		b.AddBytes(make([]byte, 32))
		b.AddUint8LengthPrefixed(
			func(b *cryptobyte.Builder) { b.AddBytes(make([]byte, f.sessionLen)) },
		)
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(f.ciphers) })
		b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(f.compression) })
		switch {
		case f.rawExts != nil:
			b.AddBytes(f.rawExts)
		case len(f.exts) > 0:
			b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
				for _, e := range f.exts {
					b.AddBytes(e)
				}
			})
		}
	})
	b.AddBytes(f.trailing)
	return b.BytesOrPanic()
}

func record(body []byte) []byte {
	b := cryptobyte.NewBuilder(nil)
	b.AddUint8(recordHandshake)
	b.AddUint16(0x0301)
	b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(body) })
	return b.BytesOrPanic()
}

func helloRecord(exts ...[]byte) []byte {
	return record(goodFields(exts...).body())
}

type parseCase struct {
	name   string
	body   []byte
	want   string
	reason Reason
}

var groupsExt = ext(extGroups, []byte{0, 2, 0, 0x1d})

func badHello(mutate func(*helloFields)) []byte {
	f := goodFields(sniExt(sniHost))
	mutate(&f)
	return f.body()
}

func acceptedCases() []parseCase {
	return []parseCase{
		{"plain", goodFields(sniExt(sniHost)).body(), sniHost, ""},
		{"mixed case", goodFields(sniExt("GitHub.COM")).body(), sniHost, ""},
		{"trailing dot", goodFields(sniExt("github.com.")).body(), sniHost, ""},
		{
			"other extensions around",
			goodFields(groupsExt, sniExt(sniHost), ext(extECHDraf-1, nil)).body(),
			sniHost,
			"",
		},
		{
			"extension just above the hiding range",
			goodFields(sniExt(sniHost), ext(extECH+1, nil)).body(),
			sniHost,
			"",
		},
	}
}

func nameCases() []parseCase {
	return []parseCase{
		{"no extensions", goodFields().body(), "", SNIMissing},
		{"only other extensions", goodFields(groupsExt).body(), "", SNIMissing},
		{"empty list", goodFields(ext(extServerName, sniData())).body(), "", SNIMissing},
		{"empty name", goodFields(sniExt("")).body(), "", SNIMissing},
		{
			"two server_name extensions",
			goodFields(sniExt(sniHost), sniExt(sniHost)).body(),
			"",
			SNIMismatch,
		},
		{"two entries", goodFields(sniExt(sniHost, "other.example.com")).body(), "", SNIMismatch},
		{"same name twice", goodFields(sniExt(sniHost, sniHost)).body(), "", SNIMismatch},
		{
			"unknown name type",
			goodFields(ext(extServerName, sniData(sniEntry{1, sniHost}))).body(),
			"",
			BadRequest,
		},
		{"ipv4 literal", goodFields(sniExt(publicAddr)).body(), "", SNIMismatch},
		{"ipv6 literal", goodFields(sniExt("2606:4700::1111")).body(), "", SNIMismatch},
		{"bracketed literal", goodFields(sniExt("[::1]")).body(), "", SNIMismatch},
		{"space in name", goodFields(sniExt("a b.com")).body(), "", SNIMismatch},
		{"nul in name", goodFields(sniExt("github.com\x00.evil.com")).body(), "", SNIMismatch},
		{"non-ascii name", goodFields(sniExt("g\u0131thub.com")).body(), "", SNIMismatch},
		{"single label", goodFields(sniExt("localhost")).body(), "", SNIMismatch},
	}
}

func hidingCases() []parseCase {
	return []parseCase{
		{
			"ech after name",
			goodFields(sniExt(sniHost), ext(extECH, []byte{0})).body(),
			"",
			BadRequest,
		},
		{"ech before name", goodFields(ext(extECH, nil), sniExt(sniHost)).body(), "", BadRequest},
		{"esni", goodFields(sniExt(sniHost), ext(extESNI, nil)).body(), "", BadRequest},
		{"ech draft", goodFields(sniExt(sniHost), ext(extECHDraf, nil)).body(), "", BadRequest},
		{"ech outer", goodFields(sniExt(sniHost), ext(extOuter, nil)).body(), "", BadRequest},
	}
}

func malformedCases() []parseCase {
	return []parseCase{
		{
			"repeated other extension",
			goodFields(groupsExt, sniExt(sniHost), groupsExt).body(),
			"",
			BadRequest,
		},
		{
			"truncated extension",
			badHello(func(f *helloFields) { f.rawExts = []byte{0, 3, 0, 0, 0} }),
			"",
			BadRequest,
		},
		{"extension block with trailing byte", badHello(func(f *helloFields) {
			e := sniExt(sniHost)
			b := cryptobyte.NewBuilder(nil)
			b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(e); b.AddUint8(0) })
			f.exts, f.rawExts = nil, b.BytesOrPanic()
		}), "", BadRequest},
		{
			"server_name data with trailing byte",
			goodFields(ext(extServerName, append(sniData(sniEntry{0, sniHost}), 0))).body(),
			"",
			BadRequest,
		},
		{
			"session id too long",
			badHello(func(f *helloFields) { f.sessionLen = 33 }),
			"",
			BadRequest,
		},
		{"no ciphers", badHello(func(f *helloFields) { f.ciphers = nil }), "", BadRequest},
		{
			"odd ciphers",
			badHello(func(f *helloFields) { f.ciphers = []byte{1, 2, 3} }),
			"",
			BadRequest,
		},
		{"no compression", badHello(func(f *helloFields) { f.compression = nil }), "", BadRequest},
		{"server hello", badHello(func(f *helloFields) { f.msgType = 2 }), "", BadRequest},
		{
			"bytes after the hello",
			badHello(func(f *helloFields) { f.trailing = []byte{1, 0, 0, 0} }),
			"",
			BadRequest,
		},
		{"empty", nil, "", BadRequest},
		{"type only", []byte{handshakeClientHello}, "", BadRequest},
		{
			"length beyond the record",
			append([]byte{handshakeClientHello, 0, 0xff, 0xff}, make([]byte, 40)...),
			"",
			BadRequest,
		},
	}
}

func (s *HelloSuite) TestParse() {
	cases := slices.Concat(acceptedCases(), nameCases(), hidingCases(), malformedCases())
	for _, c := range cases {
		name, reason := parseClientHello(c.body)
		s.Equal(c.reason, reason, c.name)
		s.Equal(c.want, name, c.name)
	}
}

func (s *HelloSuite) TestReadClientHello() {
	good := helloRecord(sniExt(sniHost))
	r := bytes.NewReader(append(append([]byte{}, good...), 0x14, 3, 3, 0, 1, 1))
	hello, reason := readClientHello(r)
	s.Empty(reason)
	s.Equal(sniHost, hello.name)
	s.Equal(good, hello.record)
	s.Equal(6, r.Len(), "bytes after the record stay unread")

	header := func(kind, major, minor byte, length uint16) []byte {
		return binary.BigEndian.AppendUint16([]byte{kind, major, minor}, length)
	}
	for name, input := range map[string][]byte{
		"empty":              nil,
		"short header":       good[:3],
		"short body":         good[:len(good)-1],
		"http":               []byte("GET / HTTP/1.1\r\nHost: github.com\r\n\r\n"),
		"ssh banner":         []byte("SSH-2.0-OpenSSH_9.6\r\n"),
		"application data":   append(header(23, 3, 3, 4), 1, 2, 3, 4),
		"alert":              append(header(21, 3, 3, 2), 2, 40),
		"ssl2 style":         append(header(22, 2, 0, 4), 1, 2, 3, 4),
		"tls minor too new":  append(header(22, 3, 5, 4), 1, 2, 3, 4),
		"zero length":        header(22, 3, 1, 0),
		"record too large":   header(22, 3, 1, maxRecordLen+1),
		"max length no data": header(22, 3, 1, maxRecordLen),
	} {
		_, reason := readClientHello(bytes.NewReader(input))
		s.Equal(BadRequest, reason, name)
	}

	r = bytes.NewReader(header(22, 3, 1, maxRecordLen+1))
	_, _ = readClientHello(r)
	s.Equal(0, r.Len(), "an oversized length is refused after the header")
}

func (s *HelloSuite) TestARecordSplitAcrossTwoIsRefused() {
	body := goodFields(sniExt(sniHost)).body()
	half := len(body) / 2
	first := record(body[:half])
	_, reason := readClientHello(bytes.NewReader(append(first, record(body[half:])...)))
	s.Equal(BadRequest, reason)
}

func (s *HelloSuite) TestRealClientsHellos() {
	for _, name := range []string{sniHost, apiHost, subdomainHost} {
		hello, reason := readClientHello(bytes.NewReader(realHello(s.T(), name)))
		s.Empty(reason, name)
		s.Equal(name, hello.name)
	}
	_, reason := readClientHello(bytes.NewReader(realHello(s.T(), publicAddr)))
	s.Equal(SNIMissing, reason, "Go sends no name for an address")
}

func (s *HelloSuite) TestAcceptedNamesMatchWhatGoReads() {
	for _, name := range []string{sniHost, apiHost, subdomainHost, "xn--bcher-kva.example"} {
		rec := realHello(s.T(), name)
		got, reached := goServerName(rec)
		s.Require().True(reached, name)
		ours, reason := parseClientHello(rec[recordHeaderLen:])
		s.Empty(reason, name)
		s.True(strings.EqualFold(got, ours), "%s vs %s", got, ours)
	}
}

func FuzzClientHello(f *testing.F) {
	for _, name := range []string{sniHost, apiHost, publicAddr} {
		f.Add(realHello(f, name))
	}
	f.Add(helloRecord(sniExt(sniHost)))
	f.Add(helloRecord(sniExt(sniHost), ext(extECH, nil)))
	f.Add(helloRecord(sniExt(sniHost, sniHost)))
	f.Add([]byte("GET / HTTP/1.1\r\n\r\n"))
	f.Fuzz(func(t *testing.T, input []byte) {
		hello, reason := readClientHello(bytes.NewReader(input))
		if reason != "" {
			return
		}
		if hello.name == "" || len(hello.record) > recordHeaderLen+maxRecordLen {
			t.Fatalf("accepted a hello with name %q and %d bytes", hello.name, len(hello.record))
		}
		if _, ok := normalizeForTest(hello.name); !ok {
			t.Fatalf("accepted name %q is not a valid host", hello.name)
		}
		if got, reached := goServerName(
			hello.record,
		); reached &&
			!strings.EqualFold(got, hello.name) {
			t.Fatalf("we read %q but Go reads %q", hello.name, got)
		}
	})
}

func normalizeForTest(name string) (string, bool) {
	normalized, reason := normalizeSNI(name)
	return normalized, reason == "" && normalized == name
}

// realHello captures the first record a Go TLS client writes.
func realHello(tb testing.TB, serverName string) []byte {
	tb.Helper()
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	_ = server.SetDeadline(time.Now().Add(5 * time.Second))
	go func() {
		_ = tls.Client(client, &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}).
			Handshake()
	}()
	var header [recordHeaderLen]byte
	if _, err := io.ReadFull(server, header[:]); err != nil {
		tb.Fatal(err)
	}
	body := make([]byte, binary.BigEndian.Uint16(header[3:]))
	if _, err := io.ReadFull(server, body); err != nil {
		tb.Fatal(err)
	}
	return append(header[:], body...)
}

// goServerName asks Go's own TLS server which name it reads from a record.
// reached is false when Go refuses the hello before it reads a name.
func goServerName(rec []byte) (name string, reached bool) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	deadline := time.Now().Add(2 * time.Second)
	_ = client.SetDeadline(deadline)
	_ = server.SetDeadline(deadline)
	go func() {
		_, _ = client.Write(rec)
		_, _ = io.Copy(io.Discard, client)
	}()
	_ = tls.Server(server, &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
			name, reached = h.ServerName, true
			return nil, errors.New("stop")
		},
	}).Handshake()
	return name, reached
}
