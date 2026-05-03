package config

import (
	"testing"
)

func TestResolvePortAttribute_ExactMatch(t *testing.T) {
	const wantLabel = "Frontend"
	attrs := map[string]PortAttribute{
		"3000": {Label: wantLabel, Protocol: ProtocolHTTPS, OnAutoForward: AutoForwardNotify},
		"5432": {OnAutoForward: AutoForwardIgnore},
	}
	got := ResolvePortAttribute(3000, attrs, nil)
	if got.Label != wantLabel {
		t.Errorf("Label = %q, want %q", got.Label, wantLabel)
	}
	if got.Protocol != ProtocolHTTPS {
		t.Errorf("Protocol = %q, want %q", got.Protocol, ProtocolHTTPS)
	}
}

func TestResolvePortAttribute_RangeMatch(t *testing.T) {
	attrs := map[string]PortAttribute{
		"8080-8090": {Label: "Dev servers", OnAutoForward: AutoForwardSilent},
	}
	got := ResolvePortAttribute(8085, attrs, nil)
	if got.Label != "Dev servers" {
		t.Errorf("Label = %q, want %q", got.Label, "Dev servers")
	}
}

func TestResolvePortAttribute_RangeBoundaries(t *testing.T) {
	const rangeLabel = "Range"
	attrs := map[string]PortAttribute{
		"8080-8090": {Label: rangeLabel},
	}
	tests := []struct {
		port    int
		wantHit bool
	}{
		{8079, false},
		{8080, true},
		{8090, true},
		{8091, false},
	}
	for _, tt := range tests {
		got := ResolvePortAttribute(tt.port, attrs, nil)
		if (got.Label == rangeLabel) != tt.wantHit {
			t.Errorf("port %d: hit=%v, want %v", tt.port, got.Label == rangeLabel, tt.wantHit)
		}
	}
}

func TestResolvePortAttribute_FallbackToOther(t *testing.T) {
	fallback := &PortAttribute{OnAutoForward: AutoForwardIgnore}
	got := ResolvePortAttribute(9999, nil, fallback)
	if got.OnAutoForward != AutoForwardIgnore {
		t.Errorf("OnAutoForward = %q, want %q", got.OnAutoForward, AutoForwardIgnore)
	}
}

func TestResolvePortAttribute_ExactTakesPrecedenceOverFallback(t *testing.T) {
	attrs := map[string]PortAttribute{
		"3000": {Label: "App", OnAutoForward: AutoForwardNotify},
	}
	fallback := &PortAttribute{OnAutoForward: AutoForwardIgnore}
	got := ResolvePortAttribute(3000, attrs, fallback)
	if got.OnAutoForward != AutoForwardNotify {
		t.Errorf("OnAutoForward = %q, want %q", got.OnAutoForward, AutoForwardNotify)
	}
}

func TestResolvePortAttribute_NoMatchNoFallback(t *testing.T) {
	attrs := map[string]PortAttribute{
		"3000": {Label: "App"},
	}
	got := ResolvePortAttribute(4000, attrs, nil)
	if got.Label != "" || got.Protocol != "" || got.OnAutoForward != "" {
		t.Errorf("expected empty PortAttribute, got %+v", got)
	}
}

func TestShouldAutoForward(t *testing.T) {
	tests := []struct {
		name string
		attr PortAttribute
		want bool
	}{
		{"empty defaults to forward", PortAttribute{}, true},
		{"notify forwards", PortAttribute{OnAutoForward: AutoForwardNotify}, true},
		{"silent forwards", PortAttribute{OnAutoForward: AutoForwardSilent}, true},
		{"ignore blocks", PortAttribute{OnAutoForward: AutoForwardIgnore}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.attr.ShouldAutoForward(); got != tt.want {
				t.Errorf("ShouldAutoForward() = %v, want %v", got, tt.want)
			}
		})
	}
}
