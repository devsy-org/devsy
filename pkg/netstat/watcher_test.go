package netstat

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockForwarder struct {
	forwarded     []string
	forwardedAttr []PortForwardAttribute
	stopped       []string
	forwardErr    error
	stopErr       error
}

func (m *mockForwarder) Forward(port string, attr PortForwardAttribute) error {
	m.forwarded = append(m.forwarded, port)
	m.forwardedAttr = append(m.forwardedAttr, attr)
	return m.forwardErr
}

func (m *mockForwarder) StopForward(port string) error {
	m.stopped = append(m.stopped, port)
	return m.stopErr
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
				OnAutoForward: AutoForwardSilent,
			}
		}
		return PortForwardAttribute{OnAutoForward: AutoForwardIgnore}
	}
	w := NewWatcher(&mockForwarder{}, WithPortAttributes(resolver))

	attr := w.resolveAttr("3000")
	assert.Equal(t, "Web App", attr.Label)
	assert.Equal(t, "https", attr.Protocol)
	assert.Equal(t, AutoForwardSilent, attr.OnAutoForward)

	attr = w.resolveAttr("9999")
	assert.Equal(t, "ignore", attr.OnAutoForward)
}

func TestWatcher_PortAttributes_IgnoreSkipsForward(t *testing.T) {
	resolver := func(port string) PortForwardAttribute {
		if port == "9501" {
			return PortForwardAttribute{OnAutoForward: AutoForwardIgnore}
		}
		return PortForwardAttribute{OnAutoForward: AutoForwardSilent, Label: "Allowed"}
	}
	w := NewWatcher(&mockForwarder{}, WithPortAttributes(resolver))

	// Verify that ignore causes skip in the resolver logic
	attr := w.resolveAttr("9501")
	assert.Equal(t, "ignore", attr.OnAutoForward)

	attr = w.resolveAttr("9500")
	assert.Equal(t, AutoForwardSilent, attr.OnAutoForward)
	assert.Equal(t, "Allowed", attr.Label)
}

func TestWatcher_OnAutoForwardSilent_Forwards(t *testing.T) {
	mf := &mockForwarder{}
	resolver := func(port string) PortForwardAttribute {
		return PortForwardAttribute{OnAutoForward: AutoForwardSilent, Label: "Silent Service"}
	}
	w := NewWatcher(mf, WithPortAttributes(resolver))

	attr := w.resolveAttr("8080")
	assert.Equal(t, AutoForwardSilent, attr.OnAutoForward)
	assert.NotEqual(t, AutoForwardIgnore, attr.OnAutoForward, "silent should not skip")
}

func TestWatcher_OnAutoForwardNotify_Forwards(t *testing.T) {
	mf := &mockForwarder{}
	resolver := func(port string) PortForwardAttribute {
		return PortForwardAttribute{OnAutoForward: "notify", Label: "Notify Service"}
	}
	w := NewWatcher(mf, WithPortAttributes(resolver))

	attr := w.resolveAttr("8080")
	assert.Equal(t, "notify", attr.OnAutoForward)
	assert.NotEqual(t, AutoForwardIgnore, attr.OnAutoForward, "notify should not skip")
}

func TestListenPortsInRange_FiltersByRange(t *testing.T) {
	socks := []SockTabEntry{
		{LocalAddr: &SockAddr{Port: 80}},    // below range
		{LocalAddr: &SockAddr{Port: 1024}},  // range start
		{LocalAddr: &SockAddr{Port: 8080}},  // in range
		{LocalAddr: &SockAddr{Port: 12000}}, // range end
		{LocalAddr: &SockAddr{Port: 13000}}, // above range
		{LocalAddr: nil},                    // malformed, must not panic
	}

	got := listenPortsInRange(socks)

	assert.Equal(t, map[string]bool{"1024": true, "8080": true, "12000": true}, got)
}

func TestListenPortsInRange_NilLocalAddrDoesNotPanic(t *testing.T) {
	socks := []SockTabEntry{{LocalAddr: nil}}

	assert.NotPanics(t, func() {
		got := listenPortsInRange(socks)
		assert.Empty(t, got)
	})
}

func TestStartPort_ForwardsWithAttr(t *testing.T) {
	mf := &mockForwarder{}
	resolver := func(port string) PortForwardAttribute {
		return PortForwardAttribute{Label: "svc", Protocol: "tcp", OnAutoForward: "notify"}
	}
	w := NewWatcher(mf, WithPortAttributes(resolver))

	err := w.startPort("8080")
	assert.NoError(t, err)
	assert.Equal(t, []string{"8080"}, mf.forwarded)
	require.Len(t, mf.forwardedAttr, 1)
	assert.Equal(t, "svc", mf.forwardedAttr[0].Label)
	assert.Equal(t, "notify", mf.forwardedAttr[0].OnAutoForward)
}

func TestStartPort_PortFilterSkips(t *testing.T) {
	mf := &mockForwarder{}
	w := NewWatcher(mf, WithPortFilter(func(port string) bool { return port != "9090" }))

	err := w.startPort("9090")
	assert.NoError(t, err)
	assert.Empty(t, mf.forwarded, "filtered port must not be forwarded")
}

func TestStartPort_OnAutoForwardIgnoreSkips(t *testing.T) {
	mf := &mockForwarder{}
	resolver := func(port string) PortForwardAttribute {
		return PortForwardAttribute{OnAutoForward: AutoForwardIgnore}
	}
	w := NewWatcher(mf, WithPortAttributes(resolver))

	err := w.startPort("8080")
	assert.NoError(t, err)
	assert.Empty(t, mf.forwarded, "ignored port must not be forwarded")
}

func TestStartPort_ForwardErrorPropagates(t *testing.T) {
	mf := &mockForwarder{forwardErr: assert.AnError}
	w := NewWatcher(mf)

	err := w.startPort("8080")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error forwarding port 8080")
}

func TestStopRemovedPorts_StopsGonePorts(t *testing.T) {
	mf := &mockForwarder{}
	w := NewWatcher(mf)
	w.forwardedPorts = map[string]bool{"8080": true, "9090": true}

	err := w.stopRemovedPorts(map[string]bool{"9090": true})
	assert.NoError(t, err)
	assert.Equal(t, []string{"8080"}, mf.stopped)
}

func TestStopRemovedPorts_StopErrorPropagates(t *testing.T) {
	mf := &mockForwarder{stopErr: assert.AnError}
	w := NewWatcher(mf)
	w.forwardedPorts = map[string]bool{"8080": true}

	err := w.stopRemovedPorts(map[string]bool{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error stop forwarding port 8080")
}

func TestStartNewPorts_StartsOnlyNewPorts(t *testing.T) {
	mf := &mockForwarder{}
	w := NewWatcher(mf)
	w.forwardedPorts = map[string]bool{"8080": true}

	err := w.startNewPorts(map[string]bool{"8080": true, "9090": true})
	assert.NoError(t, err)
	assert.Equal(
		t,
		[]string{"9090"},
		mf.forwarded,
		"already-forwarded ports must not be re-forwarded",
	)
}
