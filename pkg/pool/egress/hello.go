package egress

import (
	"encoding/binary"
	"io"
	"net/netip"

	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
	"golang.org/x/crypto/cryptobyte"
)

const (
	recordHeaderLen      = 5
	maxRecordLen         = 1 << 14
	recordHandshake      = 22
	handshakeClientHello = 1
	extServerName        = 0
	maxSessionIDLen      = 32
)

// clientHello is the first TLS record a client sent, kept whole so it can be
// forwarded unchanged, and the server name it asked for.
type clientHello struct {
	record []byte
	name   string
}

// hidesServerName reports extension types that hide or replace the server
// name: encrypted_server_name (ESNI), ech_outer_extensions and the
// encrypted_client_hello draft and final code points. The proxy cannot check
// a name it cannot read.
func hidesServerName(ext uint16) bool {
	return ext == 0xffce || ext == 0xfd00 || (ext >= 0xfe08 && ext <= 0xfe0d)
}

// readClientHello reads one TLS record and checks that it holds a whole
// ClientHello with exactly one plain host_name. It returns an empty Reason when
// the hello is acceptable. Anything that is not TLS, a hello split across
// records, extra handshake bytes in the record and a read error all return
// BadRequest, so only TLS is tunneled.
func readClientHello(r io.Reader) (clientHello, Reason) {
	var header [recordHeaderLen]byte
	if _, err := io.ReadFull(r, header[:]); err != nil || !validRecordHeader(header) {
		return clientHello{}, BadRequest
	}
	record := make([]byte, recordHeaderLen+int(binary.BigEndian.Uint16(header[3:])))
	copy(record, header[:])
	if _, err := io.ReadFull(r, record[recordHeaderLen:]); err != nil {
		return clientHello{}, BadRequest
	}
	name, reason := parseClientHello(record[recordHeaderLen:])
	if reason != "" {
		return clientHello{}, reason
	}
	return clientHello{record: record, name: name}, ""
}

func validRecordHeader(h [recordHeaderLen]byte) bool {
	length := int(binary.BigEndian.Uint16(h[3:]))
	return h[0] == recordHandshake && h[1] == 3 && h[2] <= 4 &&
		length > 0 && length <= maxRecordLen
}

// parseClientHello checks one handshake record body and returns the
// normalized server name.
func parseClientHello(body []byte) (string, Reason) {
	msg := cryptobyte.String(body)
	var msgType uint8
	var hello cryptobyte.String
	if !msg.ReadUint8(&msgType) || msgType != handshakeClientHello ||
		!msg.ReadUint24LengthPrefixed(&hello) || !msg.Empty() {
		return "", BadRequest
	}
	exts, ok := helloExtensions(&hello)
	if !ok {
		return "", BadRequest
	}
	list, found, reason := scanExtensions(exts)
	if reason != "" {
		return "", reason
	}
	if !found {
		return "", SNIMissing
	}
	return hostName(list)
}

// helloExtensions skips the fixed fields of a ClientHello and returns its
// extension block, which is empty when the hello has none.
func helloExtensions(hello *cryptobyte.String) (cryptobyte.String, bool) {
	var exts cryptobyte.String
	if !skipFixedFields(hello) {
		return nil, false
	}
	if hello.Empty() {
		return exts, true
	}
	if !hello.ReadUint16LengthPrefixed(&exts) || !hello.Empty() {
		return nil, false
	}
	return exts, true
}

// skipFixedFields steps over the version, random, session ID, cipher suites
// and compression methods, checking only their shape.
func skipFixedFields(hello *cryptobyte.String) bool {
	var session, ciphers, compression cryptobyte.String
	if !hello.Skip(2+32) || !hello.ReadUint8LengthPrefixed(&session) ||
		len(session) > maxSessionIDLen {
		return false
	}
	if !hello.ReadUint16LengthPrefixed(&ciphers) || len(ciphers) == 0 || len(ciphers)%2 != 0 {
		return false
	}
	return hello.ReadUint8LengthPrefixed(&compression) && len(compression) != 0
}

// scanExtensions walks every extension, so a hiding extension after the
// server name is still seen. It returns the server_name data when present.
func scanExtensions(exts cryptobyte.String) (cryptobyte.String, bool, Reason) {
	seen := map[uint16]bool{}
	var list cryptobyte.String
	for !exts.Empty() {
		var typ uint16
		var data cryptobyte.String
		if !exts.ReadUint16(&typ) || !exts.ReadUint16LengthPrefixed(&data) || hidesServerName(typ) {
			return nil, false, BadRequest
		}
		if seen[typ] {
			return nil, false, repeatedExtension(typ)
		}
		seen[typ] = true
		if typ == extServerName {
			list = data
		}
	}
	return list, seen[extServerName], ""
}

func repeatedExtension(typ uint16) Reason {
	if typ == extServerName {
		return SNIMismatch
	}
	return BadRequest
}

// hostName reads the server_name extension data: one host_name entry.
func hostName(data cryptobyte.String) (string, Reason) {
	var entries, name cryptobyte.String
	var nameType uint8
	if !data.ReadUint16LengthPrefixed(&entries) || !data.Empty() {
		return "", BadRequest
	}
	if entries.Empty() {
		return "", SNIMissing
	}
	if !entries.ReadUint8(&nameType) || !entries.ReadUint16LengthPrefixed(&name) {
		return "", BadRequest
	}
	if !entries.Empty() {
		return "", SNIMismatch
	}
	if nameType != 0 {
		return "", BadRequest
	}
	return normalizeSNI(string(name))
}

// normalizeSNI refuses an empty name, an IP literal and anything that is not
// a valid host name, so the result can only equal a CONNECT host name.
func normalizeSNI(name string) (string, Reason) {
	if name == "" {
		return "", SNIMissing
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return "", SNIMismatch
	}
	normalized, ok := poolconfig.NormalizeHost(name)
	if !ok {
		return "", SNIMismatch
	}
	return normalized, ""
}
