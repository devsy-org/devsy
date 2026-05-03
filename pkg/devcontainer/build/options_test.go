package build

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
)

const (
	testCacheImage    = "myregistry.io/cache:latest"
	testCLICacheImage = "cli-override:v1"
)

func substitutedConfig(cfg *config.DevContainerConfig) *config.SubstitutedConfig {
	return &config.SubstitutedConfig{Config: cfg, Raw: cfg}
}

func TestNewOptions_CacheFrom_ConfigOnly(t *testing.T) {
	params := NewOptionsParams{
		ParsedConfig: substitutedConfig(&config.DevContainerConfig{
			DockerfileContainer: config.DockerfileContainer{
				Build: &config.ConfigBuildOptions{
					CacheFrom: types.StrArray{testCacheImage, "other:tag"},
				},
			},
		}),
		Options: provider.BuildOptions{},
	}
	opts, err := NewOptions(params)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.CacheFrom) != 2 {
		t.Fatalf("expected 2 CacheFrom entries, got %d: %v", len(opts.CacheFrom), opts.CacheFrom)
	}
	if opts.CacheFrom[0] != testCacheImage || opts.CacheFrom[1] != "other:tag" {
		t.Fatalf("unexpected CacheFrom: %v", opts.CacheFrom)
	}
	if _, ok := opts.BuildArgs["BUILDKIT_INLINE_CACHE"]; ok {
		t.Fatal("BUILDKIT_INLINE_CACHE should not be set when cacheFrom is configured")
	}
}

func TestNewOptions_CacheFrom_CLIAndConfig(t *testing.T) {
	params := NewOptionsParams{
		ParsedConfig: substitutedConfig(&config.DevContainerConfig{
			DockerfileContainer: config.DockerfileContainer{
				Build: &config.ConfigBuildOptions{
					CacheFrom: types.StrArray{testCacheImage},
				},
			},
		}),
		Options: provider.BuildOptions{
			RegistryCache: "registry.example.com/cache",
		},
	}
	opts, err := NewOptions(params)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.CacheFrom) != 2 {
		t.Fatalf("expected 2 CacheFrom entries, got %d: %v", len(opts.CacheFrom), opts.CacheFrom)
	}
	if opts.CacheFrom[0] != "type=registry,ref=registry.example.com/cache" {
		t.Fatalf("expected CLI registry cache first, got: %s", opts.CacheFrom[0])
	}
	if opts.CacheFrom[1] != testCacheImage {
		t.Fatalf("expected config cache second, got: %s", opts.CacheFrom[1])
	}
}

func TestNewOptions_CacheFrom_CLIOnly(t *testing.T) {
	params := NewOptionsParams{
		ParsedConfig: substitutedConfig(&config.DevContainerConfig{}),
		Options: provider.BuildOptions{
			CLIOptions: provider.CLIOptions{
				CacheFrom: []string{"cli-cache:latest"},
			},
		},
	}
	opts, err := NewOptions(params)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.CacheFrom) != 1 {
		t.Fatalf("expected 1 CacheFrom entry, got %d: %v", len(opts.CacheFrom), opts.CacheFrom)
	}
	if opts.CacheFrom[0] != "cli-cache:latest" {
		t.Fatalf("expected CLI cache-from value, got: %s", opts.CacheFrom[0])
	}
	if _, ok := opts.BuildArgs["BUILDKIT_INLINE_CACHE"]; ok {
		t.Fatal("BUILDKIT_INLINE_CACHE should not be set when CLI cacheFrom is configured")
	}
}

func TestNewOptions_CacheFrom_CLIOverridesConfig(t *testing.T) {
	params := NewOptionsParams{
		ParsedConfig: substitutedConfig(&config.DevContainerConfig{
			DockerfileContainer: config.DockerfileContainer{
				Build: &config.ConfigBuildOptions{
					CacheFrom: types.StrArray{testCacheImage, "other:tag"},
				},
			},
		}),
		Options: provider.BuildOptions{
			CLIOptions: provider.CLIOptions{
				CacheFrom: []string{testCLICacheImage},
			},
		},
	}
	opts, err := NewOptions(params)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.CacheFrom) != 1 {
		t.Fatalf(
			"expected 1 CacheFrom entry (CLI overrides config), got %d: %v",
			len(opts.CacheFrom),
			opts.CacheFrom,
		)
	}
	if opts.CacheFrom[0] != testCLICacheImage {
		t.Fatalf("expected CLI cache-from to override config, got: %s", opts.CacheFrom[0])
	}
}

func TestNewOptions_CacheFrom_CLIOverridesConfigWithRegistryCache(t *testing.T) {
	params := NewOptionsParams{
		ParsedConfig: substitutedConfig(&config.DevContainerConfig{
			DockerfileContainer: config.DockerfileContainer{
				Build: &config.ConfigBuildOptions{
					CacheFrom: types.StrArray{testCacheImage},
				},
			},
		}),
		Options: provider.BuildOptions{
			CLIOptions: provider.CLIOptions{
				CacheFrom: []string{testCLICacheImage},
			},
			RegistryCache: "registry.example.com/cache",
		},
	}
	opts, err := NewOptions(params)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.CacheFrom) != 2 {
		t.Fatalf("expected 2 CacheFrom entries, got %d: %v", len(opts.CacheFrom), opts.CacheFrom)
	}
	if opts.CacheFrom[0] != "type=registry,ref=registry.example.com/cache" {
		t.Fatalf("expected registry cache first, got: %s", opts.CacheFrom[0])
	}
	if opts.CacheFrom[1] != testCLICacheImage {
		t.Fatalf("expected CLI cache-from second (overriding config), got: %s", opts.CacheFrom[1])
	}
}

func TestNewOptions_CacheFrom_Fallback(t *testing.T) {
	params := NewOptionsParams{
		ParsedConfig: substitutedConfig(&config.DevContainerConfig{}),
		Options:      provider.BuildOptions{},
	}
	opts, err := NewOptions(params)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.CacheFrom) != 0 {
		t.Fatalf("expected empty CacheFrom, got: %v", opts.CacheFrom)
	}
	if opts.BuildArgs["BUILDKIT_INLINE_CACHE"] != "1" {
		t.Fatal("expected BUILDKIT_INLINE_CACHE=1 as fallback")
	}
}
