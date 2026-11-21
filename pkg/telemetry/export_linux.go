package telemetry

import (
	"errors"
	"io"
	"os"
	"syscall"
	"time"
)

func appendMetricsFile(path string, data []byte) error {
	fd, err := syscall.Open(
		path,
		syscall.O_WRONLY|syscall.O_APPEND|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_NONBLOCK,
		0o600,
	)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), "metrics")
	defer func() { _ = file.Close() }()
	if err := validateMetricsFile(file); err != nil {
		return err
	}
	if err := lockMetricsFile(fd); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(fd, syscall.LOCK_UN) }()
	n, err := file.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

func validateMetricsFile(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 ||
		int64(stat.Uid) != int64(os.Geteuid()) {
		return errors.New("unsafe metrics file")
	}
	return nil
}

func lockMetricsFile(fd int) error {
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		if time.Now().After(deadline) {
			return errors.New("metrics lock timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
