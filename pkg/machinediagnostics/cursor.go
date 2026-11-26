package machinediagnostics

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

type cursor struct {
	sessionID string
	sequence  uint64
}

func EncodeCursor(sessionID string, sequence uint64) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(fmt.Sprintf("v1:%s:%d", sessionID, sequence)),
	)
}

func decodeCursor(value string) (cursor, error) {
	if value == "" {
		return cursor{}, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor{}, fmt.Errorf("decode cursor: %w", err)
	}
	p := strings.Split(string(b), ":")
	if len(p) != 3 || p[0] != "v1" || p[1] == "" {
		return cursor{}, fmt.Errorf("invalid cursor")
	}
	n, err := strconv.ParseUint(p[2], 10, 64)
	if err != nil {
		return cursor{}, fmt.Errorf("invalid cursor")
	}
	return cursor{p[1], n}, nil
}

// ValidateCursor keeps cursor encoding opaque to callers while allowing public
// command surfaces to reject malformed input before opening a remote session.
func ValidateCursor(value string) error {
	_, err := decodeCursor(value)
	return err
}
