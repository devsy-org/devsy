package ssh

import (
	"testing"

	"github.com/stretchr/testify/assert"
	gossh "golang.org/x/crypto/ssh"
)

func TestHandleKeepAliveRequests(t *testing.T) {
	requests := make(chan *gossh.Request, 2)
	requests <- &gossh.Request{Type: keepAliveRequestType}
	other := &gossh.Request{Type: "other"}
	requests <- other
	close(requests)

	forwarded := handleKeepAliveRequests(requests)
	assert.Same(t, other, <-forwarded)
	_, open := <-forwarded
	assert.False(t, open)
}
