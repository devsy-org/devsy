//go:build linux || darwin

package dependencies

import (
	"errors"
	"os"
	"syscall"
)

func metadataWriteSupported() error { return nil }

func validatePrivateMetadata(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int64(stat.Uid) != int64(os.Geteuid()) || info.Mode().Perm()&0o077 != 0 {
		return errors.New("metadata must be private to the current user")
	}
	return nil
}

func openMetadataFile(root *os.Root, name string) (*os.File, error) {
	// NONBLOCK avoids a substituted FIFO hanging a metadata read; NOFOLLOW refuses
	// destination links, including links whose targets are still inside the root.
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

func syncMetadataDirectory(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
