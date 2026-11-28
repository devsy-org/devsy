// Package channel carries egress decisions from the sidecar to the pool over
// one Unix socket per session. The socket says which session a decision
// belongs to, so no line names one. A line is one JSON object with a closed
// set of fields, and the pool checks every value again, so a bad sidecar can
// send the pool nothing but well-formed decisions.
//
// The socket is for the sidecar only. Never mount it into the agent's
// container: whoever can connect can send decisions for the session.
package channel

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/netip"

	"github.com/devsy-org/devsy/pkg/pool/egress"
	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
)

const (
	// MaxLine is the longest line, newline included.
	MaxLine = 512
	// MaxRule is the largest rule index a line may carry.
	MaxRule = 1<<16 - 1
	maxPort = 65535

	kindDeny  = "deny"
	kindAllow = "allow"
	kindReady = "ready"
)

// Kind says what a line reports.
type Kind int

const (
	// KindDeny is a refused request.
	KindDeny Kind = iota + 1
	// KindAllow is a tunnel that passed every check.
	KindAllow
	// KindReady says the sidecar's proxy is listening.
	KindReady
)

// Event is one decoded line. Denial is set for KindDeny and Allow for
// KindAllow.
type Event struct {
	Kind   Kind
	Denial egress.Denial
	Allow  egress.Allow
}

var (
	errInvalid     = errors.New("invalid egress event")
	errUnencodable = errors.New("egress decision cannot be sent")
)

type line struct {
	Kind     string `json:"t"`
	Host     string `json:"host,omitempty"`
	Redacted bool   `json:"redacted,omitempty"`
	Port     int    `json:"port,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Peer     string `json:"peer,omitempty"`
	Rule     *int   `json:"rule,omitempty"`
}

// EncodeDenial returns the line for a refusal, newline included. A host the
// pool would not accept is sent as redacted and a peer it would not accept is
// left out.
func EncodeDenial(d egress.Denial) ([]byte, error) {
	if !d.Reason.Valid() || d.Port < 0 || d.Port > maxPort {
		return nil, errUnencodable
	}
	l := line{Kind: kindDeny, Reason: string(d.Reason), Port: d.Port, Peer: peerText(d.Peer)}
	l.Host, l.Redacted = hostFields(d.Target)
	return marshal(l)
}

// EncodeAllow returns the line for an allowed tunnel, newline included.
func EncodeAllow(a egress.Allow) ([]byte, error) {
	if a.Port < 1 || a.Port > maxPort {
		return nil, errUnencodable
	}
	l := line{Kind: kindAllow, Port: a.Port}
	l.Host, l.Redacted = hostFields(a.Target)
	if index, ok := a.Rule.Index(); ok && index >= 0 && index <= MaxRule {
		l.Rule = &index
	}
	return marshal(l)
}

func encodeReady() []byte {
	out, _ := marshal(line{Kind: kindReady})
	return out
}

func marshal(l line) ([]byte, error) {
	out, err := json.Marshal(l)
	if err != nil || len(out)+1 > MaxLine {
		return nil, errUnencodable
	}
	return append(out, '\n'), nil
}

func hostFields(t egress.Target) (string, bool) {
	if t.AddressRedacted || !validHost(t.Host) {
		return "", true
	}
	return t.Host, false
}

func peerText(addr netip.Addr) string {
	if !validPeer(addr) {
		return ""
	}
	return addr.String()
}

// Decode checks one line, with or without its newline. Every error is the
// same fixed text, so a caller can log it without echoing the line.
func Decode(raw []byte) (Event, error) {
	raw = bytes.TrimSuffix(raw, []byte("\n"))
	if len(raw) == 0 || len(raw)+1 > MaxLine {
		return Event{}, errInvalid
	}
	var l line
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&l) != nil || dec.Decode(&struct{}{}) != io.EOF {
		return Event{}, errInvalid
	}
	ev, err := decodeLine(l)
	if err != nil {
		return Event{}, err
	}
	// Only the exact bytes an encoder writes pass, so a duplicate key, a
	// differently cased key, a reordered field or an escaped letter is
	// refused rather than read as something else.
	if canon, err := json.Marshal(l); err != nil || !bytes.Equal(canon, raw) {
		return Event{}, errInvalid
	}
	return ev, nil
}

func decodeLine(l line) (Event, error) {
	switch l.Kind {
	case kindDeny:
		return decodeDeny(l)
	case kindAllow:
		return decodeAllow(l)
	case kindReady:
		if l != (line{Kind: kindReady}) {
			return Event{}, errInvalid
		}
		return Event{Kind: KindReady}, nil
	}
	return Event{}, errInvalid
}

func decodeDeny(l line) (Event, error) {
	target, ok := decodeTarget(l, 0)
	reason := egress.Reason(l.Reason)
	if !ok || l.Rule != nil || !reason.Valid() {
		return Event{}, errInvalid
	}
	d := egress.Denial{Target: target, Reason: reason}
	if l.Peer != "" {
		peer, err := netip.ParseAddr(l.Peer)
		if err != nil || !validPeer(peer) || peer.String() != l.Peer {
			return Event{}, errInvalid
		}
		d.Peer = peer
	}
	return Event{Kind: KindDeny, Denial: d}, nil
}

func decodeAllow(l line) (Event, error) {
	target, ok := decodeTarget(l, 1)
	if !ok || l.Reason != "" || l.Peer != "" {
		return Event{}, errInvalid
	}
	a := egress.Allow{Target: target}
	if l.Rule != nil {
		if *l.Rule < 0 || *l.Rule > MaxRule {
			return Event{}, errInvalid
		}
		a.Rule = egress.RuleAt(*l.Rule)
	}
	return Event{Kind: KindAllow, Allow: a}, nil
}

func decodeTarget(l line, minPort int) (egress.Target, bool) {
	if l.Port < minPort || l.Port > maxPort {
		return egress.Target{}, false
	}
	if l.Redacted {
		return egress.Target{Port: l.Port, AddressRedacted: true}, l.Host == ""
	}
	return egress.Target{Host: l.Host, Port: l.Port}, validHost(l.Host)
}

// validHost accepts the two forms the proxy records: a normalized DNS name,
// or an IP literal in its canonical text with no zone.
func validHost(host string) bool {
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Zone() == "" && addr.String() == host
	}
	name, ok := poolconfig.NormalizeHost(host)
	return ok && name == host
}

func validPeer(addr netip.Addr) bool {
	return addr.IsValid() && addr.Zone() == "" && !addr.Is4In6()
}
