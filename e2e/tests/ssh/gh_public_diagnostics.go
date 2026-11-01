package ssh

import (
	"bytes"
	"context"
	"sync"
	"time"

	"github.com/onsi/ginkgo/v2"
)

type publicSessionStderr struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *publicSessionStderr) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if remaining := 8192 - b.buffer.Len(); len(p) > remaining {
		p = p[:remaining]
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

func (b *publicSessionStderr) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func (p *publicGHFixture) registerDiagnostics(
	t ginkgo.GinkgoTInterface,
	workspace string,
	deleted *bool,
) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() || *deleted {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, _, err := p.f.ExecCommandCapture(ctx, []string{
			cmdWorkspace,
			cmdSSH,
			workspace,
			flagNoServices,
			flagCommand,
			"head -c 4096 /tmp/fixture-api.log; pgrep -a ghfixture; stat -c '%a %u %g %n' /fixture/api-ready /fixture/cert.pem",
		})
		t.Logf("public API fixture diagnostics: %s (error: %v)", out, err)
	})
}
