package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

const (
	dockerlessBuilderConfig = "/builder/docker"
	dockerlessParseFailure  = "parse"
)

type dockerlessEnvironmentCase struct {
	name             string
	builderConfig    string
	builderConfigSet bool
	imageConfig      string
	imageConfigSet   bool
}

func TestBuildAndApplyContainerEnv_ImageEnvironmentWins(t *testing.T) {
	for _, tc := range []dockerlessEnvironmentCase{
		{
			name:             "replace builder config",
			builderConfig:    dockerlessBuilderConfig,
			builderConfigSet: true,
			imageConfig:      "/image/docker",
			imageConfigSet:   true,
		},
		{name: "set image config", imageConfig: "/image/docker", imageConfigSet: true},
		{name: "keep builder config when image omits it", builderConfig: dockerlessBuilderConfig, builderConfigSet: true},
		{name: "keep config absent when image omits it"},
		{
			name:             "empty image config wins",
			builderConfig:    dockerlessBuilderConfig,
			builderConfigSet: true,
			imageConfigSet:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertDockerlessImageEnvironment(t, tc)
		})
	}
}

type dockerlessEnvironmentFixture struct {
	builderPath, imagePath, toolName, toolPath, credentialsDir string
	credentialsDone                                            <-chan struct{}
	cleanup                                                    func()
}

func setupDockerlessEnvironmentTest(
	t *testing.T,
	tc dockerlessEnvironmentCase,
) dockerlessEnvironmentFixture {
	t.Helper()
	builderPath := filepath.Join(t.TempDir(), "builder-bin")
	imageBin := t.TempDir()
	imagePath := strings.Join(
		[]string{imageBin, "/usr/bin", "/bin"},
		string(os.PathListSeparator),
	)
	t.Setenv("PATH", builderPath)
	setDockerlessTestConfig(t, tc.builderConfig, tc.builderConfigSet)

	toolName, toolPath := createDockerlessImageExecutable(t, imageBin)
	_, lookupErr := exec.LookPath(toolName)
	require.Error(t, lookupErr, "image-only executable unexpectedly found in builder PATH")

	credentialsDir := filepath.Join(t.TempDir(), "credentials")
	var credentialsDone <-chan struct{}
	cleanup := setupDockerCredentials(DockerlessBuildOptions{
		Context:           context.Background(),
		DockerlessOptions: &provider2.ProviderDockerlessOptions{},
		ConfigureCredentialsFunc: func(ctx context.Context) (string, error) {
			credentialsDone = ctx.Done()
			require.NoError(t, os.Mkdir(credentialsDir, 0o700))
			require.NoError(
				t,
				os.Setenv("PATH", builderPath+string(os.PathListSeparator)+credentialsDir),
			)
			require.NoError(t, os.Setenv("DOCKER_CONFIG", credentialsDir))
			return credentialsDir, nil
		},
	})
	require.NotNil(t, cleanup, "expected credentials cleanup")
	return dockerlessEnvironmentFixture{
		builderPath:     builderPath,
		imagePath:       imagePath,
		toolName:        toolName,
		toolPath:        toolPath,
		credentialsDir:  credentialsDir,
		credentialsDone: credentialsDone,
		cleanup:         cleanup,
	}
}

func createDockerlessImageExecutable(t *testing.T, imageBin string) (string, string) {
	t.Helper()
	// This executable exists only in the image's PATH, never the builder's.
	toolName := "devsy-image-only-tool"
	if runtime.GOOS == "windows" {
		toolName += ".exe"
	}
	toolPath := filepath.Join(imageBin, toolName)
	require.NoError(t, os.WriteFile(
		toolPath,
		[]byte("#!/bin/sh\nprintf 'image-only-tool\\n'\n"),
		0o600,
	))
	// #nosec G302 -- owner-only executable fixture in a test-owned temporary directory.
	require.NoError(t, os.Chmod(toolPath, 0o700))
	return toolName, toolPath
}

func assertDockerlessImageEnvironment(t *testing.T, tc dockerlessEnvironmentCase) {
	t.Helper()
	fixture := setupDockerlessEnvironmentTest(t, tc)
	cleanupCalls := 0
	var sequence []string
	err := buildAndApplyContainerEnv(func() {
		cleanupCalls++
		sequence = append(sequence, "cleanup")
		fixture.cleanup()
	}, func() error {
		sequence = append(sequence, "build")
		require.Equal(
			t,
			fixture.builderPath+string(os.PathListSeparator)+fixture.credentialsDir,
			os.Getenv("PATH"),
			"build PATH",
		)
		require.Equal(t, fixture.credentialsDir, os.Getenv("DOCKER_CONFIG"), "build DOCKER_CONFIG")
		assertDockerlessCredentialsCanceled(t, fixture.credentialsDone, false)
		return nil
	}, func() error {
		sequence = append(sequence, "apply")
		require.Equal(t, fixture.builderPath, os.Getenv("PATH"), "PATH before image environment")
		assertDockerlessTestConfig(t, tc.builderConfig, tc.builderConfigSet)
		assertDockerlessCredentialsCanceled(t, fixture.credentialsDone, true)
		_, statErr := os.Stat(fixture.credentialsDir)
		require.ErrorIs(t, statErr, os.ErrNotExist)
		if err := os.Setenv("PATH", fixture.imagePath); err != nil {
			return err
		}
		if tc.imageConfigSet {
			return os.Setenv("DOCKER_CONFIG", tc.imageConfig)
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "build,cleanup,apply", strings.Join(sequence, ","))
	require.Equal(t, 1, cleanupCalls)
	require.Equal(t, fixture.imagePath, os.Getenv("PATH"), "runtime PATH")
	require.NotContains(t, os.Getenv("PATH"), fixture.credentialsDir)
	if tc.imageConfigSet {
		assertDockerlessTestConfig(t, tc.imageConfig, true)
	} else {
		assertDockerlessTestConfig(t, tc.builderConfig, tc.builderConfigSet)
	}
	got, lookupErr := exec.LookPath(fixture.toolName)
	require.NoError(t, lookupErr)
	require.Equal(t, fixture.toolPath, got)
}

func TestBuildAndApplyContainerEnv_RuntimePathSurvivesCleanup(t *testing.T) {
	t.Setenv("PATH", "/builder/bin")
	setDockerlessTestConfig(t, dockerlessBuilderConfig, true)
	credentialsDir := t.TempDir()
	cleanup := setupDockerCredentials(DockerlessBuildOptions{
		Context:           context.Background(),
		DockerlessOptions: &provider2.ProviderDockerlessOptions{},
		ConfigureCredentialsFunc: func(context.Context) (string, error) {
			if err := os.Setenv("PATH", "/temporary/helper"); err != nil {
				return "", err
			}
			return credentialsDir, os.Setenv("DOCKER_CONFIG", credentialsDir)
		},
	})
	if cleanup == nil {
		t.Fatal("expected credentials cleanup")
	}
	err := buildAndApplyContainerEnv(cleanup, func() error { return nil }, func() error {
		if err := os.Setenv("PATH", "/image/bin:/usr/bin:/bin"); err != nil {
			return err
		}
		return os.Setenv("DOCKER_CONFIG", "/image/docker")
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("PATH"); got != "/image/bin:/usr/bin:/bin" {
		t.Fatalf("runtime PATH = %q, want image PATH", got)
	}
	assertDockerlessTestConfig(t, "/image/docker", true)
}

func TestBuildAndApplyContainerEnv_BuildError(t *testing.T) {
	for _, buildErr := range []error{errors.New("build failed"), context.Canceled} {
		t.Run(buildErr.Error(), func(t *testing.T) {
			t.Setenv("PATH", "/builder/bin")
			setDockerlessTestConfig(t, dockerlessBuilderConfig, true)
			credentialsDir := t.TempDir()
			var credentialsDone <-chan struct{}
			cleanup := setupDockerCredentials(DockerlessBuildOptions{
				Context:           context.Background(),
				DockerlessOptions: &provider2.ProviderDockerlessOptions{},
				ConfigureCredentialsFunc: func(ctx context.Context) (string, error) {
					credentialsDone = ctx.Done()
					_ = os.Setenv("PATH", "/temporary/helper")
					_ = os.Setenv("DOCKER_CONFIG", credentialsDir)
					return credentialsDir, nil
				},
			})
			cleanupCalls, applyCalls := 0, 0
			err := buildAndApplyContainerEnv(func() {
				cleanupCalls++
				cleanup()
			}, func() error {
				return buildErr
			}, func() error {
				applyCalls++
				return nil
			})
			if err != buildErr || cleanupCalls != 1 || applyCalls != 0 {
				t.Fatalf(
					"error = %v, cleanup calls = %d, apply calls = %d",
					err,
					cleanupCalls,
					applyCalls,
				)
			}
			if got := os.Getenv("PATH"); got != "/builder/bin" {
				t.Fatalf("PATH after failed build = %q", got)
			}
			assertDockerlessTestConfig(t, dockerlessBuilderConfig, true)
			assertDockerlessCredentialsCanceled(t, credentialsDone, true)
			if _, err := os.Stat(credentialsDir); !os.IsNotExist(err) {
				t.Fatalf("credentials directory still present after failed build: %v", err)
			}
		})
	}
}

func TestBuildAndApplyContainerEnv_ImageError(t *testing.T) {
	for _, failure := range []string{"read", dockerlessParseFailure} {
		t.Run(failure, func(t *testing.T) {
			imageConfig := filepath.Join(t.TempDir(), "image.json")
			if failure == dockerlessParseFailure {
				if err := os.WriteFile(
					imageConfig,
					[]byte("invalid image config"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			cleanupCalls := 0
			err := buildAndApplyContainerEnv(func() { cleanupCalls++ }, func() error {
				return nil
			}, func() error {
				require.Equal(t, 1, cleanupCalls, "image application started before cleanup")
				return applyContainerEnv(imageConfig)
			})
			require.Error(t, err)
			require.Equal(t, 1, cleanupCalls)
			if failure == "read" {
				require.ErrorIs(t, err, os.ErrNotExist)
			}
			if failure == dockerlessParseFailure {
				require.ErrorContains(t, err, "parse container config")
			}
		})
	}
}

func TestBuildAndApplyContainerEnv_NoCleanup(t *testing.T) {
	applyCalls := 0
	if err := buildAndApplyContainerEnv(nil, func() error { return nil }, func() error {
		applyCalls++
		return nil
	}); err != nil || applyCalls != 1 {
		t.Fatalf("error = %v, apply calls = %d", err, applyCalls)
	}
}

func TestBuildAndApplyContainerEnv_PanicCleanup(t *testing.T) {
	for _, stage := range []string{"build", "cleanup", "apply"} {
		t.Run(stage, func(t *testing.T) {
			cleanupCalls := 0
			defer func() {
				if got := recover(); got != stage {
					t.Fatalf("panic = %v, want %q", got, stage)
				}
				if cleanupCalls != 1 {
					t.Fatalf("cleanup calls = %d, want 1", cleanupCalls)
				}
			}()
			_ = buildAndApplyContainerEnv(func() {
				cleanupCalls++
				if stage == "cleanup" {
					panic(stage)
				}
			}, func() error {
				if stage == "build" {
					panic(stage)
				}
				return nil
			}, func() error {
				if stage == "apply" {
					panic(stage)
				}
				return nil
			})
		})
	}
}

func TestSetupDockerCredentials_NoCleanup(t *testing.T) {
	for _, mode := range []string{"disabled", "missing callback", "configuration failed"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("PATH", "/builder/bin")
			setDockerlessTestConfig(t, "", false)
			opts := DockerlessBuildOptions{
				Context:           context.Background(),
				DockerlessOptions: &provider2.ProviderDockerlessOptions{},
			}
			var credentialsDone <-chan struct{}
			switch mode {
			case "disabled":
				opts.DockerlessOptions.DisableDockerCredentials = trueValue
				opts.ConfigureCredentialsFunc = func(context.Context) (string, error) {
					t.Fatal("disabled credentials callback called")
					return "", nil
				}
			case "configuration failed":
				opts.ConfigureCredentialsFunc = func(ctx context.Context) (string, error) {
					credentialsDone = ctx.Done()
					_ = os.Setenv("PATH", "/temporary/helper")
					_ = os.Setenv("DOCKER_CONFIG", "/temporary/config")
					return "", errors.New("configuration failed")
				}
			}
			if cleanup := setupDockerCredentials(opts); cleanup != nil {
				t.Fatal("unexpected cleanup")
			}
			if got := os.Getenv("PATH"); got != "/builder/bin" {
				t.Fatalf("PATH = %q", got)
			}
			assertDockerlessTestConfig(t, "", false)
			if credentialsDone != nil {
				assertDockerlessCredentialsCanceled(t, credentialsDone, true)
			}
		})
	}
}

func setDockerlessTestConfig(t *testing.T, value string, present bool) {
	t.Helper()
	t.Setenv("DOCKER_CONFIG", value)
	if !present {
		if err := os.Unsetenv("DOCKER_CONFIG"); err != nil {
			t.Fatal(err)
		}
	}
}

func assertDockerlessTestConfig(t *testing.T, value string, present bool) {
	t.Helper()
	if got, set := os.LookupEnv("DOCKER_CONFIG"); got != value || set != present {
		t.Fatalf("DOCKER_CONFIG = %q, present %v; want %q, present %v", got, set, value, present)
	}
}

func assertDockerlessCredentialsCanceled(t *testing.T, done <-chan struct{}, want bool) {
	t.Helper()
	canceled := false
	select {
	case <-done:
		canceled = true
	default:
	}
	require.Equal(t, want, canceled, "credentials context cancellation")
}
