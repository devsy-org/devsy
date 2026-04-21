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
	return &connectionCounter{
		ctx:       ctx,
		address:   address,
		timeout:   timeout,
		onTimeout: onTimeout,
	}
}

type connectionCounter struct {
	address string

	ctx       context.Context
	timeout   time.Duration
	onTimeout func()

	m           sync.Mutex
	connections int
	generation  int
}

func (c *connectionCounter) Add() {
	c.m.Lock()
	defer c.m.Unlock()

	c.connections++
	log.Debugf("New connection on %s (Total: %d)", c.address, c.connections)
}

func (c *connectionCounter) Dec() {
	c.m.Lock()
	defer c.m.Unlock()

	c.connections--
	log.Debugf("Closed connection on %s (Total: %d)", c.address, c.connections)
	if c.connections <= 0 && c.timeout > 0 {
		c.generation++

		go func(generation int) {
			select {
			case <-c.ctx.Done():
				return
			case <-time.After(c.timeout):
				c.m.Lock()
				defer c.m.Unlock()

				if c.generation == generation && c.connections <= 0 {
					c.onTimeout()
				}
			}
		}(c.generation)
	}
}
