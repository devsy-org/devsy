package workspacelogs

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type cursorKind string

const (
	cursorRun   cursorKind = "run"
	cursorIndex cursorKind = "index"
)

// cursor is the decoded form of an opaque page token. A run cursor binds the
// run, the record filters and the last sequence read from each producer. An
// index cursor binds the selectors and the ordering position. Wall-clock time
// never decides what was already read.
type cursor struct {
	Version int               `json:"v"`
	Kind    cursorKind        `json:"k"`
	Run     string            `json:"r,omitempty"`
	Filter  string            `json:"f,omitempty"`
	Pos     map[string]uint64 `json:"p,omitempty"`
	// Gaps remembers that an earlier page reported a gap, so the last page of a
	// read cannot claim completeness.
	Gaps    bool      `json:"g,omitempty"`
	Context string    `json:"c,omitempty"`
	UID     string    `json:"u,omitempty"`
	At      time.Time `json:"t,omitzero"`
	ID      string    `json:"i,omitempty"`
}

func (c *cursor) encode() (string, error) {
	if len(c.Pos) > MaxProducers {
		return "", fmt.Errorf("%w: too many producers", ErrInvalidCursor)
	}
	data, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(data)
	if len(token) > MaxCursorBytes {
		return "", fmt.Errorf("%w: cursor exceeds %d bytes", ErrInvalidCursor, MaxCursorBytes)
	}

	return token, nil
}

// decodeCursor parses token. An empty token is the start position.
func decodeCursor(token string, kind cursorKind) (*cursor, error) {
	if token == "" {
		return &cursor{Version: SchemaVersion, Kind: kind}, nil
	}
	if len(token) > MaxCursorBytes {
		return nil, fmt.Errorf("%w: cursor exceeds %d bytes", ErrInvalidCursor, MaxCursorBytes)
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("%w: not a cursor", ErrInvalidCursor)
	}
	var c cursor
	if err = json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("%w: not a cursor", ErrInvalidCursor)
	}
	if c.Version != SchemaVersion || c.Kind != kind || len(c.Pos) > MaxProducers {
		return nil, fmt.Errorf("%w: wrong version, kind or size", ErrInvalidCursor)
	}

	return &c, nil
}

// filterHash binds a cursor to the selectors it was produced under.
func filterHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = fmt.Fprintf(h, "%d:%s;", len(p), p)
	}

	return hex.EncodeToString(h.Sum(nil))[:16]
}
