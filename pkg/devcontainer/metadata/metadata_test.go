package metadata

import (
	"fmt"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

func TestFeatureConfigToImageMetadata_IncludesContainerEnv(t *testing.T) {
	feature := &config.FeatureConfig{
		ContainerEnv: map[string]string{
			"FOO": "bar",
			"BAZ": "qux",
		},
	}

	got := FeatureConfigToImageMetadata(feature)

	if got.ContainerEnv == nil {
		t.Fatal("expected ContainerEnv to be set, got nil")
	}
	if got.ContainerEnv["FOO"] != "bar" {
		t.Errorf("ContainerEnv[FOO] = %q, want %q", got.ContainerEnv["FOO"], "bar")
	}
	if got.ContainerEnv["BAZ"] != "qux" {
		t.Errorf("ContainerEnv[BAZ] = %q, want %q", got.ContainerEnv["BAZ"], "qux")
	}
}

func TestMarshalImageMetadata_ContainerEnvRoundTrip(t *testing.T) {
	original := []*config.ImageMetadata{
		{
			NonComposeBase: config.NonComposeBase{
				ContainerEnv: map[string]string{
					"PATH_EXT": "/usr/local/bin",
					"MY_VAR":   "hello",
				},
			},
		},
	}

	data, err := MarshalImageMetadata(original)
	if err != nil {
		t.Fatalf("MarshalImageMetadata: %v", err)
	}

	if !strings.Contains(string(data), `"PATH_EXT":"/usr/local/bin"`) &&
		!strings.Contains(string(data), `"PATH_EXT": "/usr/local/bin"`) {
		t.Errorf("serialized data missing PATH_EXT: %s", string(data))
	}
	if !strings.Contains(string(data), `"MY_VAR"`) {
		t.Errorf("serialized data missing MY_VAR: %s", string(data))
	}
}

func TestMarshalImageMetadata_NoWarningWhenSmall(t *testing.T) {
	small := []*config.ImageMetadata{
		{
			NonComposeBase: config.NonComposeBase{
				ContainerEnv: map[string]string{"A": "B"},
			},
		},
	}

	data, err := MarshalImageMetadata(small)
	if err != nil {
		t.Fatalf("MarshalImageMetadata: %v", err)
	}
	if len(data) > metadataLabelSizeWarningThreshold {
		t.Fatalf("test data should be small, got %d bytes", len(data))
	}
}

func TestMarshalImageMetadata_WarnsWhenLarge(t *testing.T) {
	largeEnv := make(map[string]string)
	for i := range 5000 {
		key := fmt.Sprintf("ENV_VAR_%05d", i)
		largeEnv[key] = strings.Repeat("x", 20)
	}

	large := []*config.ImageMetadata{
		{
			NonComposeBase: config.NonComposeBase{
				ContainerEnv: largeEnv,
			},
		},
	}

	data, err := MarshalImageMetadata(large)
	if err != nil {
		t.Fatalf("MarshalImageMetadata: %v", err)
	}
	if len(data) <= metadataLabelSizeWarningThreshold {
		t.Fatalf("test data should exceed threshold, got %d bytes", len(data))
	}
}
