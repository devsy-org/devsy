//go:build !windows

package local

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

func GetSocketPath() string {
	return fmt.Sprintf("/tmp/%s", socketSuffix)
}

func Dial() (net.Conn, error) {
	return net.DialTimeout("unix", GetSocketPath(), 2*time.Second)
}

func listen(addr string) (net.Listener, error) {
	conn, err := net.Dial("unix", addr)
	if err == nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%s: address already in use", addr)
	}
	_ = os.Remove(addr)

	sockDir := filepath.Dir(addr)
	if _, err := os.Stat(sockDir); errors.Is(err, os.ErrNotExist) {
		_ = os.MkdirAll(sockDir, 0o755) //nolint:gosec // socket dir in /tmp
	}
	pipe, err := net.Listen("unix", addr)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(addr, 0o666) //nolint:gosec // allow any local user to connect
	return pipe, err
}
