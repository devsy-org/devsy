package session

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
)

const (
	idPrefix    = "p-"
	idSuffixLen = 10
	// maxIDLength is the workspace ID limit.
	maxIDLength = 48
)

var (
	idEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").
			WithPadding(base32.NoPadding)
	idPattern = regexp.MustCompile(`^p-[a-z0-9]([a-z0-9-]*[a-z0-9])?-[a-z2-7]{10}$`)

	errInvalidPool = errors.New("pool name is not valid")
	errInvalidID   = errors.New("session ID is not valid")
)

// randomSource feeds session ID suffixes. It is crypto/rand, and a variable
// only so a test can make it fail.
var randomSource io.Reader = rand.Reader

// NewID returns p-<pool>-<10 random base32 characters>. The suffix is an
// unguessable name for the session's workspace and files.
func NewID(pool string) (string, error) {
	if !poolconfig.ValidPoolName(pool) {
		return "", errInvalidPool
	}
	var raw [7]byte
	if _, err := io.ReadFull(randomSource, raw[:]); err != nil {
		return "", fmt.Errorf("generate session ID: %w", err)
	}
	return idPrefix + pool + "-" + idEncoding.EncodeToString(raw[:])[:idSuffixLen], nil
}

// ValidID reports whether id has the shape NewID returns. It is the only
// check between a caller-supplied ID and a file name.
func ValidID(id string) bool {
	return len(id) <= maxIDLength && idPattern.MatchString(id)
}
