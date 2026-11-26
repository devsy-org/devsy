package docker

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/devsy-org/devsy/pkg/pool/provider/providertest"
)

// simProvider pairs a Provider with the simulated daemon behind it so the
// harness can reach the daemon.
type simProvider struct {
	*Provider
	d *daemon
}

func TestContractAgainstTheSimulatedDaemon(t *testing.T) {
	providertest.RunContract(t, providertest.Harness{
		New: func(*testing.T) provider.Provider {
			d := &daemon{}
			return &simProvider{Provider: New(d), d: d}
		},
		Seed: func(_ *testing.T, p provider.Provider, r provider.Resource) {
			p.(*simProvider).d.seed(r.Kind, r.Name, r.Labels)
		},
		OnNextCall: func(_ *testing.T, p provider.Provider, fn func()) {
			d := p.(*simProvider).d
			d.mu.Lock()
			defer d.mu.Unlock()
			d.once = fn
		},
		Inventory: func(_ *testing.T, p provider.Provider) []provider.Resource {
			return p.(*simProvider).d.inventory()
		},
	})
}
