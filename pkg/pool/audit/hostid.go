package audit

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/oklog/ulid/v2"
)

const (
	hostIDPrefix       = "h_"
	hostIDLockSuffix   = ".lock"
	maxHostIDFileBytes = 128
)

var (
	hostIDPattern = regexp.MustCompile(`^h_[0-9A-HJKMNP-TV-Z]{26}$`)
	// hostIDPartialPattern matches the empty string or a cut-short prefix of
	// a host ID, the shapes a crash can leave behind. A ULID starts with 0-7.
	hostIDPartialPattern = regexp.MustCompile(`^(h(_([0-7][0-9A-HJKMNP-TV-Z]{0,24})?)?)?$`)

	errHostIDPartial = errors.New("host id file is empty or partial")
)

// NewID returns a new ULID string, the dedupe key of a record. The random part
// comes from crypto/rand, which does not return an error on supported Go
// versions, so MustNew cannot panic here.
func NewID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// LoadOrCreateHostID returns the local host ID stored at path, minting
// h_<ULID> on first use. A file that is empty or holds only the start of a
// host ID, left by a crash, is replaced with a fresh ID. Any other content is
// an error: a real ID is never replaced, so earlier records keep correlating.
// Callers in separate processes agree on one ID through a lock next to path.
// The directory must pass the same owner and mode check as the audit
// directory, and the file must be a regular file, not a link.
func LoadOrCreateHostID(path string) (string, error) {
	id, _, err := loadOrCreateHostID(path)
	// TODO: write an audit record when the ID was repaired, once the shared
	// audit contract defines the event.
	return id, err
}

func loadOrCreateHostID(path string) (id string, repaired bool, err error) {
	dir := filepath.Dir(path)
	if err := checkDirMode(dir); err != nil {
		return "", false, err
	}
	lock, err := newFlock(path + hostIDLockSuffix)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = lock.Close() }()
	if err := acquire(lock, defaultLockWait, false); err != nil {
		return "", false, err
	}
	id, err = readHostID(path)
	switch {
	case err == nil:
		// An earlier call may have renamed the file and failed to sync.
		if err := syncDirFunc(dir); err != nil {
			return "", false, fmt.Errorf("sync host id directory: %w", err)
		}
		return id, false, nil
	case errors.Is(err, fs.ErrNotExist):
		id, err = publishHostID(path)
		return id, false, err
	case errors.Is(err, errHostIDPartial):
		id, err = publishHostID(path)
		return id, err == nil, err
	}
	return "", false, err
}

func readHostID(path string) (string, error) {
	if err := checkRegular(path); err != nil {
		return "", err
	}
	f, err := os.OpenFile(filepath.Clean(path), os.O_RDONLY|noFollow|nonBlock, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if _, err := statRegular(f); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxHostIDFileBytes+1))
	if err != nil {
		return "", fmt.Errorf("read host id file: %w", err)
	}
	if len(data) <= maxHostIDFileBytes {
		if id := strings.TrimSpace(string(data)); hostIDPattern.MatchString(id) {
			return id, nil
		}
		if hostIDPartialPattern.MatchString(strings.TrimFunc(string(data), isBlank)) {
			return "", errHostIDPartial
		}
	}
	return "", fmt.Errorf("host id file %s does not hold a host ID", path)
}

// publishHostID syncs the ID into a temporary file, renames it over path and
// syncs the directory, so a reader never sees a half-written file and the
// result survives a crash.
func publishHostID(path string) (string, error) {
	id := hostIDPrefix + NewID()
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".host-id-*")
	if err != nil {
		return "", fmt.Errorf("create host id file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := writeSynced(tmp, id+"\n"); err != nil {
		return "", fmt.Errorf("write host id file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("publish host id file: %w", err)
	}
	if err := syncDirFunc(dir); err != nil {
		return "", fmt.Errorf("sync host id directory: %w", err)
	}
	return id, nil
}

func isBlank(r rune) bool { return r == 0 || unicode.IsSpace(r) }

func writeSynced(f *os.File, content string) error {
	_, err := f.WriteString(content)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
