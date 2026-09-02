package ssh

import (
	"context"
	"testing"
)

func TestOpenSessionConnRequiresInputs(t *testing.T) {
	if _, err := OpenSessionConn(context.Background(), nil, SessionConnOptions{Command: "cat"}); err == nil {
		t.Fatal("nil client should fail")
	}
}
