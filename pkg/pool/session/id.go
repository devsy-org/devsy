package session

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
)

const (
	idPrefix    = "p-"
	idSuffixLen = 10
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
	if !idPattern.MatchString(id) {
		return false
	}
	return poolconfig.ValidPoolName(id[len(idPrefix) : len(id)-idSuffixLen-1])
}

// OwnsID reports whether id is valid and belongs to exactly pool.
func OwnsID(pool, id string) bool {
	prefix := idPrefix + pool + "-"
	return len(id) == len(prefix)+idSuffixLen && strings.HasPrefix(id, prefix) && ValidID(id)
}
