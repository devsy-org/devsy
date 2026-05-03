package config

import "testing"

const testAutoForwardSilent = "silent"

func TestShouldAutoForward_NilReceiver(t *testing.T) {
	var pa *PortAttribute
	if !pa.ShouldAutoForward() {
		t.Fatal("nil PortAttribute must allow forwarding")
	}
}

func TestShouldAutoForward_Ignore(t *testing.T) {
	pa := &PortAttribute{OnAutoForward: onAutoForwardIgnore}
	if pa.ShouldAutoForward() {
		t.Fatal("onAutoForward=ignore must suppress forwarding")
	}
}

func TestShouldAutoForward_Defaults(t *testing.T) {
	for _, value := range []string{"", "notify", "openBrowser", testAutoForwardSilent} {
		pa := &PortAttribute{OnAutoForward: value}
		if !pa.ShouldAutoForward() {
			t.Errorf("onAutoForward=%q must allow forwarding", value)
		}
	}
}

func TestResolvePortAttribute_ExplicitMatch(t *testing.T) {
	attrs := map[string]PortAttribute{
		"8080": {OnAutoForward: onAutoForwardIgnore, Label: "web"},
	}
	other := &PortAttribute{OnAutoForward: testAutoForwardSilent}

	got := ResolvePortAttribute("8080", attrs, other)
	if got.OnAutoForward != onAutoForwardIgnore || got.Label != "web" {
		t.Fatalf("expected explicit attrs, got %+v", got)
	}
}

func TestResolvePortAttribute_FallbackToOther(t *testing.T) {
	attrs := map[string]PortAttribute{
		"8080": {OnAutoForward: onAutoForwardIgnore},
	}
	other := &PortAttribute{OnAutoForward: testAutoForwardSilent, Label: "default"}

	got := ResolvePortAttribute("3000", attrs, other)
	if got != other {
		t.Fatalf("expected otherPortsAttributes, got %+v", got)
	}
}

func TestResolvePortAttribute_NilOther(t *testing.T) {
	attrs := map[string]PortAttribute{}
	got := ResolvePortAttribute("3000", attrs, nil)
	if got != nil {
		t.Fatalf("expected nil when no match and no defaults, got %+v", got)
	}
}

func TestResolvePortAttribute_EmptyMap(t *testing.T) {
	other := &PortAttribute{OnAutoForward: "notify"}
	got := ResolvePortAttribute("9090", nil, other)
	if got != other {
		t.Fatalf("expected otherPortsAttributes with nil map, got %+v", got)
	}
}
