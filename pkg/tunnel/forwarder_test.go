package tunnel

import (
	"context"
	"testing"

	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/assert"
)

func TestNewPortAttributeResolver_ExactPort(t *testing.T) {
	attrs := map[string]config2.PortAttribute{
		"3000": {Label: "Web UI", Protocol: config2.ProtocolHTTPS},
	}
	resolver := NewPortAttributeResolver(attrs, nil)

	got := resolver("3000")
	assert.Equal(t, "Web UI", got.Label)
	assert.Equal(t, config2.ProtocolHTTPS, got.Protocol)
}

func TestNewPortAttributeResolver_RangePort(t *testing.T) {
	attrs := map[string]config2.PortAttribute{
		"8080-8090": {Label: "Dev servers"},
	}
	resolver := NewPortAttributeResolver(attrs, nil)

	got := resolver("8085")
	assert.Equal(t, "Dev servers", got.Label)
}

func TestNewPortAttributeResolver_FallbackToOther(t *testing.T) {
	fallback := &config2.PortAttribute{OnAutoForward: config2.AutoForwardIgnore}
	resolver := NewPortAttributeResolver(nil, fallback)

	got := resolver("9999")
	assert.Equal(t, config2.AutoForwardIgnore, got.OnAutoForward)
	assert.False(t, got.ShouldAutoForward())
}

func TestNewPortAttributeResolver_InvalidPort(t *testing.T) {
	attrs := map[string]config2.PortAttribute{
		"3000": {Label: "App"},
	}
	resolver := NewPortAttributeResolver(attrs, nil)

	got := resolver("notaport")
	assert.Equal(t, config2.PortAttribute{}, got)
}

func TestForwarder_SkipsIgnoredPort(t *testing.T) {
	resolver := func(_ string) config2.PortAttribute {
		return config2.PortAttribute{OnAutoForward: config2.AutoForwardIgnore}
	}
	f := &forwarder{
		forwardedPorts: nil,
		portMap:        map[string]context.CancelFunc{},
		resolver:       resolver,
	}
	attr := f.resolveAttr("3000")
	assert.False(t, attr.ShouldAutoForward())
}

func TestForwarder_ResolveAttr_NilResolver(t *testing.T) {
	f := &forwarder{
		resolver: nil,
		portMap:  map[string]context.CancelFunc{},
	}
	attr := f.resolveAttr("3000")
	assert.Equal(t, config2.PortAttribute{}, attr)
	assert.True(t, attr.ShouldAutoForward())
}

func TestForwarder_RequireLocalPort_Available(t *testing.T) {
	attrs := map[string]config2.PortAttribute{
		"3000": {RequireLocalPort: true, Label: "App"},
	}
	resolver := NewPortAttributeResolver(attrs, nil)
	got := resolver("3000")
	assert.True(t, got.RequireLocalPort)
	assert.Equal(t, "App", got.Label)
}

func TestForwarder_Protocol_HTTPS(t *testing.T) {
	attrs := map[string]config2.PortAttribute{
		"443": {Protocol: config2.ProtocolHTTPS},
	}
	resolver := NewPortAttributeResolver(attrs, nil)
	got := resolver("443")
	assert.Equal(t, config2.ProtocolHTTPS, got.Protocol)
}
