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
	timerToken  *connectionTimerToken
	closed      bool
	timingOut   bool
}

type connectionTimerToken struct{}

func (c *connectionCounter) Add() bool {
	c.m.Lock()
	defer c.m.Unlock()

	if c.closed || c.timingOut {
		return false
	}
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.timerToken = nil
	c.connections++
	log.Debugf("New connection on %s (Total: %d)", c.address, c.connections)
	return true
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
	c.timerToken = nil
}

func (c *connectionCounter) armTimeoutLocked() {
	if !c.canArmTimeoutLocked() {
		return
	}
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	token := new(connectionTimerToken)
	c.timerToken = token
	c.timer = time.AfterFunc(c.timeout, func() {
		c.handleTimeout(token)
	})
}

func (c *connectionCounter) canArmTimeoutLocked() bool {
	if c.closed || c.timingOut || c.connections != 0 {
		return false
	}
	if c.timeout <= 0 {
		return false
	}
	return c.ctx.Err() == nil
}

func (c *connectionCounter) handleTimeout(token *connectionTimerToken) {
	c.m.Lock()
	if c.timerToken != token {
		c.m.Unlock()
		return
	}
	if !c.canArmTimeoutLocked() {
		c.m.Unlock()
		return
	}
	c.timingOut = true
	c.timer = nil
	c.timerToken = nil
	onTimeout := c.onTimeout
	c.m.Unlock()
	onTimeout()
}
