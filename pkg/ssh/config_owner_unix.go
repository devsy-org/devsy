//go:build !windows

package ssh

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/devsy-org/devsy/pkg/log"
)

var errOwnerMismatch = errors.New("cannot preserve ssh config owner")

func preserveConfigOwner(target string, temp *os.File) error {
	err := chownLike(target, temp)
	if errors.Is(err, errOwnerMismatch) {
		return err
	}
	if err != nil {
		log.Debugf("preserve ssh config owner: %v", err)
	}

	return nil
}

func chownLike(target string, temp *os.File) error {
	want, found, err := statOwner(target)
	if err != nil || !found {
		return err
	}
	created, err := temp.Stat()
	if err != nil {
		return err
	}

	have, haveOK := fileOwner(created)
	if !haveOK || want == have {
		return nil
	}

	err = temp.Chown(int(want[0]), int(want[1]))

	return classifyChownError(err, want[0] != have[0])
}

func classifyChownError(err error, uidDiffers bool) error {
	switch {
	case err == nil:
		return nil
	case uidDiffers:
		return fmt.Errorf("%w: %w", errOwnerMismatch, err)
	case errors.Is(err, fs.ErrPermission):
		return nil
	default:
		return err
	}
}

func statOwner(target string) ([2]uint32, bool, error) {
	info, err := os.Stat(filepath.Clean(target))
	if errors.Is(err, fs.ErrNotExist) {
		return [2]uint32{}, false, nil
	}
	if err != nil {
		return [2]uint32{}, false, err
	}
	owner, ok := fileOwner(info)

	return owner, ok, nil
}

func fileOwner(info os.FileInfo) ([2]uint32, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return [2]uint32{}, false
	}

	return [2]uint32{stat.Uid, stat.Gid}, true
}
