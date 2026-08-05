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

func TestShouldAutoForward_OpenBrowserVariants(t *testing.T) {
	tests := []struct {
		name string
		attr PortAttribute
		want bool
	}{
		{"openBrowser forwards", PortAttribute{OnAutoForward: AutoForwardOpenBrowser}, true},
		{"openBrowserOnce forwards", PortAttribute{OnAutoForward: AutoForwardOpenBrowserOnce}, true},
		{"openPreview forwards", PortAttribute{OnAutoForward: AutoForwardOpenPreview}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.attr.ShouldAutoForward(); got != tt.want {
				t.Errorf("ShouldAutoForward() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsOpenBrowserAction(t *testing.T) {
	tests := []struct {
		name string
		attr PortAttribute
		want bool
	}{
		{"notify is not open-browser", PortAttribute{OnAutoForward: AutoForwardNotify}, false},
		{"silent is not open-browser", PortAttribute{OnAutoForward: AutoForwardSilent}, false},
		{"ignore is not open-browser", PortAttribute{OnAutoForward: AutoForwardIgnore}, false},
		{"empty is not open-browser", PortAttribute{}, false},
		{"openBrowser is open-browser", PortAttribute{OnAutoForward: AutoForwardOpenBrowser}, true},
		{"openBrowserOnce is open-browser", PortAttribute{OnAutoForward: AutoForwardOpenBrowserOnce}, true},
		{"openPreview is open-browser", PortAttribute{OnAutoForward: AutoForwardOpenPreview}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.attr.IsOpenBrowserAction(); got != tt.want {
				t.Errorf("IsOpenBrowserAction() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsOpenOnceAction(t *testing.T) {
	tests := []struct {
		name string
		attr PortAttribute
		want bool
	}{
		{"openBrowserOnce is once", PortAttribute{OnAutoForward: AutoForwardOpenBrowserOnce}, true},
		{"openBrowser is not once", PortAttribute{OnAutoForward: AutoForwardOpenBrowser}, false},
		{"openPreview is not once", PortAttribute{OnAutoForward: AutoForwardOpenPreview}, false},
		{"notify is not once", PortAttribute{OnAutoForward: AutoForwardNotify}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.attr.IsOpenOnceAction(); got != tt.want {
				t.Errorf("IsOpenOnceAction() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolvePortAttribute_RegexKeyMatch(t *testing.T) {
	attrs := map[string]PortAttribute{
		"^30\\d\\d$": {Label: "Three-thousands", OnAutoForward: AutoForwardSilent},
	}
	got := ResolvePortAttribute(3042, attrs, nil)
	if got.Label != "Three-thousands" {
		t.Errorf("Label = %q, want %q", got.Label, "Three-thousands")
	}
}

func TestResolvePortAttribute_RegexKeyNoMatch(t *testing.T) {
	attrs := map[string]PortAttribute{
		"^30\\d\\d$": {Label: "Three-thousands"},
	}
	got := ResolvePortAttribute(4042, attrs, nil)
	if got.Label != "" {
		t.Errorf("Label = %q, want empty (no match)", got.Label)
	}
}

func TestResolvePortAttribute_ExactBeatsRegex(t *testing.T) {
	attrs := map[string]PortAttribute{
		"^30\\d\\d$": {Label: "Regex match"},
		"3042":       {Label: "Exact match"},
	}
	got := ResolvePortAttribute(3042, attrs, nil)
	if got.Label != "Exact match" {
		t.Errorf("Label = %q, want %q (exact key should win over regex)", got.Label, "Exact match")
	}
}

func TestResolvePortAttribute_RangeBeatsRegex(t *testing.T) {
	attrs := map[string]PortAttribute{
		"^30\\d\\d$": {Label: "Regex match"},
		"3040-3050":  {Label: "Range match"},
	}
	got := ResolvePortAttribute(3042, attrs, nil)
	if got.Label != "Range match" {
		t.Errorf("Label = %q, want %q (range key should win over regex)", got.Label, "Range match")
	}
}

func TestResolvePortAttribute_InvalidRegexKeyIgnored(t *testing.T) {
	attrs := map[string]PortAttribute{
		"[invalid(regex": {Label: "Should never match"},
	}
	got := ResolvePortAttribute(3042, attrs, nil)
	if got.Label != "" {
		t.Errorf("Label = %q, want empty (invalid regex must not panic or match)", got.Label)
	}
}
