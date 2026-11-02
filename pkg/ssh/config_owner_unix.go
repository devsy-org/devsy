//go:build !windows

package ssh

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/devsy-org/devsy/pkg/log"
)

func preserveConfigOwner(target string, temp *os.File) {
	err := chownLike(target, temp)
	if err != nil {
		log.Debugf("preserve ssh config owner: %v", err)
	}
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
	if err != nil && !errors.Is(err, fs.ErrPermission) {
		return err
	}

	return nil
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
