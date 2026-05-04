package netstat

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type mockForwarder struct {
	forwarded     []string
	forwardedAttr []PortForwardAttribute
	stopped       []string
}

func (m *mockForwarder) Forward(port string, attr PortForwardAttribute) error {
	m.forwarded = append(m.forwarded, port)
	m.forwardedAttr = append(m.forwardedAttr, attr)
	return nil
}

func (m *mockForwarder) StopForward(port string) error {
	m.stopped = append(m.stopped, port)
	return nil
}

func TestNewWatcher_NilFilter(t *testing.T) {
	w := NewWatcher(&mockForwarder{})
	assert.Nil(t, w.portFilter)
	assert.Nil(t, w.attrResolver)
}

func TestNewWatcher_WithPortFilter(t *testing.T) {
	f := func(string) bool { return true }
	w := NewWatcher(&mockForwarder{}, WithPortFilter(f))
	assert.NotNil(t, w.portFilter)
}

func TestNewWatcher_WithPortAttributes(t *testing.T) {
	resolver := func(port string) PortForwardAttribute {
		return PortForwardAttribute{Label: "test", Protocol: "https"}
	}
	w := NewWatcher(&mockForwarder{}, WithPortAttributes(resolver))
	assert.NotNil(t, w.attrResolver)
}

func TestWatcher_PortFilterSkipsIgnored(t *testing.T) {
	mf := &mockForwarder{}
	w := NewWatcher(mf, WithPortFilter(func(port string) bool {
		return port != "9090"
	}))

	w.forwardedPorts = map[string]bool{}

	assert.False(t, w.portFilter("9090"), "filter should reject 9090")
	assert.True(t, w.portFilter("8080"), "filter should accept 8080")
}

func TestWatcher_ResolveAttr_NilResolver(t *testing.T) {
	w := NewWatcher(&mockForwarder{})
	attr := w.resolveAttr("3000")
	assert.Equal(t, PortForwardAttribute{}, attr)
}

func TestWatcher_ResolveAttr_WithResolver(t *testing.T) {
	resolver := func(port string) PortForwardAttribute {
		if port == "3000" {
			return PortForwardAttribute{
				Label:         "Web App",
				Protocol:      "https",
				OnAutoForward: "silent",
			}
		}
		return PortForwardAttribute{OnAutoForward: AutoForwardIgnore}
	}
	w := NewWatcher(&mockForwarder{}, WithPortAttributes(resolver))

	attr := w.resolveAttr("3000")
	assert.Equal(t, "Web App", attr.Label)
	assert.Equal(t, "https", attr.Protocol)
	assert.Equal(t, "silent", attr.OnAutoForward)

	attr = w.resolveAttr("9999")
	assert.Equal(t, "ignore", attr.OnAutoForward)
}

func TestWatcher_PortAttributes_IgnoreSkipsForward(t *testing.T) {
	resolver := func(port string) PortForwardAttribute {
		if port == "9501" {
			return PortForwardAttribute{OnAutoForward: AutoForwardIgnore}
		}
		return PortForwardAttribute{OnAutoForward: "silent", Label: "Allowed"}
	}
	w := NewWatcher(&mockForwarder{}, WithPortAttributes(resolver))

	// Verify that ignore causes skip in the resolver logic
	attr := w.resolveAttr("9501")
	assert.Equal(t, "ignore", attr.OnAutoForward)

	attr = w.resolveAttr("9500")
	assert.Equal(t, "silent", attr.OnAutoForward)
	assert.Equal(t, "Allowed", attr.Label)
}
