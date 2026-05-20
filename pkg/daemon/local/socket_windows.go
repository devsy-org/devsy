//go:build windows

package local

import (
	"fmt"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

func GetSocketPath() string {
	return fmt.Sprintf("\\\\.\\pipe\\%s", socketSuffix)
}

func Dial() (net.Conn, error) {
	timeout := 2 * time.Second
	return winio.DialPipe(GetSocketPath(), &timeout)
}

func listen(addr string) (net.Listener, error) {
	return winio.ListenPipe(addr, nil)
}
