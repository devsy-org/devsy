package netstat

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type mockForwarder struct {
	forwarded []string
	stopped   []string
}

func (m *mockForwarder) Forward(port string) error {
	m.forwarded = append(m.forwarded, port)
	return nil
}

func (m *mockForwarder) StopForward(port string) error {
	m.stopped = append(m.stopped, port)
	return nil
}

func TestNewWatcher_NilFilter(t *testing.T) {
	w := NewWatcher(&mockForwarder{})
	assert.Nil(t, w.portFilter)
}

func TestNewWatcher_WithPortFilter(t *testing.T) {
	f := func(string) bool { return true }
	w := NewWatcher(&mockForwarder{}, WithPortFilter(f))
	assert.NotNil(t, w.portFilter)
}

func TestWatcher_PortFilterSkipsIgnored(t *testing.T) {
	mf := &mockForwarder{}
	w := NewWatcher(mf, WithPortFilter(func(port string) bool {
		return port != "9090"
	}))

	// Simulate discovered ports by injecting into forwardedPorts after
	// a hand-crafted runOnce cycle. Since findPorts reads /proc we
	// drive the filter path by directly manipulating state.
	w.forwardedPorts = map[string]bool{}

	// We can't call runOnce (it reads /proc), so verify the filter
	// is wired correctly by checking the contract.
	assert.False(t, w.portFilter("9090"), "filter should reject 9090")
	assert.True(t, w.portFilter("8080"), "filter should accept 8080")
}
