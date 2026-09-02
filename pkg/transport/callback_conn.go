package transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"
)

type Callback func(ctx context.Context, stdin io.Reader, stdout io.Writer) error

type CallbackConnOptions struct {
	LocalAddr  net.Addr
	RemoteAddr net.Addr
}

type callbackConn struct {
	local  net.Conn
	remote net.Conn
	cancel context.CancelFunc
	state  *managedState
}

func OpenCallbackConn(ctx context.Context, callback Callback, opts CallbackConnOptions) (ManagedConn, error) {
	if callback == nil {
		return nil, fmt.Errorf("callback is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	local, remote := net.Pipe()
	callbackCtx, cancel := context.WithCancel(ctx)
	conn := &callbackConn{
		local:  local,
		remote: remote,
		cancel: cancel,
		state:  newManagedState(),
	}
	callbackDone := make(chan struct{})
	go func() {
		select {
		case <-callbackCtx.Done():
			_ = conn.Close()
		case <-callbackDone:
		}
	}()
	go func() {
		defer close(callbackDone)
		err := callback(callbackCtx, remote, remote)
		conn.state.finish(err)
		_ = remote.Close()
	}()
	return &addrConn{Conn: conn, local: opts.LocalAddr, remote: opts.RemoteAddr}, nil
}

func (c *callbackConn) Read(p []byte) (int, error)  { return c.local.Read(p) }
func (c *callbackConn) Write(p []byte) (int, error) { return c.local.Write(p) }

func (c *callbackConn) Close() error {
	var err error
	c.state.closeOnce.Do(func() {
		c.cancel()
		if closeErr := c.local.Close(); closeErr != nil {
			err = closeErr
		}
		if closeErr := c.remote.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	})
	return err
}

func (c *callbackConn) LocalAddr() net.Addr                { return netPipeAddr{} }
func (c *callbackConn) RemoteAddr() net.Addr               { return netPipeAddr{} }
func (c *callbackConn) SetDeadline(t time.Time) error      { return c.local.SetDeadline(t) }
func (c *callbackConn) SetReadDeadline(t time.Time) error  { return c.local.SetReadDeadline(t) }
func (c *callbackConn) SetWriteDeadline(t time.Time) error { return c.local.SetWriteDeadline(t) }
func (c *callbackConn) Wait() error                        { return c.state.wait() }

type netPipeAddr struct{}

func (netPipeAddr) Network() string { return "devsy-transport" }
func (netPipeAddr) String() string  { return "callback" }

type addrConn struct {
	net.Conn
	local  net.Addr
	remote net.Addr
}

func (c *addrConn) LocalAddr() net.Addr {
	if c.local != nil {
		return c.local
	}
	return c.Conn.LocalAddr()
}

func (c *addrConn) RemoteAddr() net.Addr {
	if c.remote != nil {
		return c.remote
	}
	return c.Conn.RemoteAddr()
}

func (c *addrConn) Wait() error {
	return c.Conn.(ManagedConn).Wait()
}

func (c *addrConn) CloseWrite() error {
	if closeWriter, ok := c.Conn.(CloseWriter); ok {
		return closeWriter.CloseWrite()
	}
	return net.ErrClosed
}

func (c *addrConn) PID() int {
	if process, ok := c.Conn.(ProcessInfo); ok {
		return process.PID()
	}
	return 0
}
