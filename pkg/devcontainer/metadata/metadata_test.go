package metadata

import (
	"fmt"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

func TestFeatureConfigToImageMetadata_ExcludesContainerEnv(t *testing.T) {
	feature := &config.FeatureConfig{
		ContainerEnv: map[string]string{
			"FOO": "bar",
			"BAZ": "qux",
		},
	}

	got := FeatureConfigToImageMetadata(feature)

	if got.ContainerEnv != nil {
		t.Fatalf(
			"expected ContainerEnv to NOT be in per-feature metadata (applied via Dockerfile ENV only), got %v",
			got.ContainerEnv,
		)
	}
}

func TestGetImageMetadataFromContainer_WarnsOnParseFailure(t *testing.T) {
	details := &config.ContainerDetails{
		Config: config.ContainerDetailsConfig{
			Labels: map[string]string{
				ImageMetadataLabel: "not valid json {{{",
			},
		},
	}

	result, err := GetImageMetadataFromContainer(details, &config.SubstitutionContext{})
	if err != nil {
		t.Fatalf("expected no error on corrupt metadata, got: %v", err)
	}
	if len(result.Raw) != 0 {
		t.Fatalf("expected empty Raw slice for corrupt metadata, got %d entries", len(result.Raw))
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

func TestGetDevContainerMetadata_PreservesRawLocalEnvMount(t *testing.T) {
	raw := &config.DevContainerConfig{
		NonComposeBase: config.NonComposeBase{
			Mounts: []*config.Mount{
				{
					Type:   "bind",
					Source: "${localEnv:HOME}/.cache/example",
					Target: "/cache/example",
				},
			},
		},
	}

	effective := &config.DevContainerConfig{
		NonComposeBase: config.NonComposeBase{
			Mounts: []*config.Mount{
				{
					Type:   "bind",
					Source: "/home/test/.cache/example",
					Target: "/cache/example",
				},
			},
		},
	}

	substituted := &config.SubstitutedConfig{
		Raw:    raw,
		Config: effective,
	}

	got, err := GetDevContainerMetadata(
		&config.SubstitutionContext{},
		&config.ImageMetadataConfig{},
		substituted,
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got.Raw) == 0 || len(got.Raw[len(got.Raw)-1].Mounts) == 0 {
		t.Fatal("expected non-empty got.Raw mounts")
	}
	if len(got.Config) == 0 || len(got.Config[len(got.Config)-1].Mounts) == 0 {
		t.Fatal("expected non-empty got.Config mounts")
	}

	rawSource := got.Raw[len(got.Raw)-1].Mounts[0].Source
	expectedRaw := "${localEnv:HOME}/.cache/example"
	if rawSource != expectedRaw {
		t.Errorf("got raw source %q, want %q", rawSource, expectedRaw)
	}

	configSource := got.Config[len(got.Config)-1].Mounts[0].Source
	expectedConfig := "/home/test/.cache/example"
	if configSource != expectedConfig {
		t.Errorf("got config source %q, want %q", configSource, expectedConfig)
	}
}
