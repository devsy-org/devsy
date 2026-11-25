package audit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/oklog/ulid/v2"
)

const hostIDPrefix = "h_"

var hostIDPattern = regexp.MustCompile(`^h_[0-9A-HJKMNP-TV-Z]{26}$`)

// NewID returns a new ULID string, the dedupe key of a record.
func NewID() string {
	return ulid.Make().String()
}

// LoadOrCreateHostID returns the local host ID stored at path, minting
// h_<ULID> on first use. A stored value that is not a host ID is an error:
// the ID is never replaced, so earlier records keep correlating.
func LoadOrCreateHostID(path string) (string, error) {
	id, err := readHostID(path)
	if errors.Is(err, fs.ErrNotExist) {
		return createHostID(path)
	}
	return id, err
}

func readHostID(path string) (string, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(data))
	if !hostIDPattern.MatchString(id) {
		return "", fmt.Errorf("host id file %s does not hold a host ID", path)
	}
	return id, nil
}

// createHostID writes the ID to a temporary file and links it into place, so
// a concurrent first start never reads a half-written file and only one
// minted ID wins.
func createHostID(path string) (string, error) {
	id := hostIDPrefix + NewID()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".host-id-*")
	if err != nil {
		return "", fmt.Errorf("create host id file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, werr := tmp.WriteString(id + "\n")
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return "", fmt.Errorf("write host id file: %w", werr)
	}
	err = os.Link(tmp.Name(), path)
	if errors.Is(err, fs.ErrExist) {
		return readHostID(path)
	}
	if err != nil {
		return "", fmt.Errorf("create host id file: %w", err)
	}
	return id, nil
}
