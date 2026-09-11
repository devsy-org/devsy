package ssh

import (
	"context"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
)

func newConnectionCounter(
	ctx context.Context,
	timeout time.Duration,
	onTimeout func(),
	address string,
) *connectionCounter {
	c := &connectionCounter{
		ctx:       ctx,
		address:   address,
		timeout:   timeout,
		onTimeout: onTimeout,
	}
	c.m.Lock()
	c.armTimeoutLocked()
	c.m.Unlock()
	return c
}

type connectionCounter struct {
	address string

	ctx       context.Context
	timeout   time.Duration
	onTimeout func()

	m           sync.Mutex
	connections int
	timer       *time.Timer
	closed      bool
}

func (c *connectionCounter) armTimeoutLocked() {
	if c.closed || c.connections != 0 || c.timeout <= 0 || c.ctx.Err() != nil {
		return
	}
	if c.timer != nil {
		c.timer.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(c.timeout, func() {
		c.m.Lock()
		if c.timer != timer || c.closed || c.connections != 0 || c.ctx.Err() != nil {
			c.m.Unlock()
			return
		}
		c.timer = nil
		onTimeout := c.onTimeout
		c.m.Unlock()
		onTimeout()
	})
	c.timer = timer
}

func (c *connectionCounter) Add() {
	c.m.Lock()
	defer c.m.Unlock()

	if c.closed {
		return
	}
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.connections++
	log.Debugf("New connection on %s (Total: %d)", c.address, c.connections)
}

func (c *connectionCounter) Dec() {
	c.m.Lock()
	defer c.m.Unlock()

	if c.closed {
		return
	}
	if c.connections <= 0 {
		c.connections = 0
	} else {
		c.connections--
	}
	log.Debugf("Closed connection on %s (Total: %d)", c.address, c.connections)
	c.armTimeoutLocked()
}

func (c *connectionCounter) Close() {
	c.m.Lock()
	defer c.m.Unlock()
	c.closed = true
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
}
