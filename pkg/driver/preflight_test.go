package driver

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

func TestPreflightErrorUnwrapAndMessage(t *testing.T) {
	cause := errors.New("Cannot connect to Podman")
	perr := &PreflightError{Provider: "podman", Err: cause}

	if perr.Error() != cause.Error() {
		t.Fatalf("Error() = %q, want %q", perr.Error(), cause.Error())
	}
	if !errors.Is(perr, cause) {
		t.Fatal("errors.Is did not unwrap to the cause")
	}

	var target *PreflightError
	if !errors.As(error(perr), &target) {
		t.Fatal("errors.As did not recognize *PreflightError")
	}
	if target.Provider != "podman" {
		t.Fatalf("Provider = %q, want podman", target.Provider)
	}
}

func TestAutoStartDisabledByEnv(t *testing.T) {
	for _, tc := range []struct {
		val  string
		want bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"1", true},
		{"true", true},
		{"garbage", false},
	} {
		t.Setenv(NoAutoStartEnv, tc.val)
		if got := AutoStartDisabledByEnv(); got != tc.want {
			t.Errorf("AutoStartDisabledByEnv() with %q = %v, want %v", tc.val, got, tc.want)
		}
	}
}

// noPreflightDriver implements Driver but not Preflighter.
type noPreflightDriver struct{}

func (noPreflightDriver) FindDevContainer(
	context.Context,
	string,
) (*config.ContainerDetails, error) {
	return nil, nil
}
func (noPreflightDriver) CommandDevContainer(context.Context, *CommandParams) error { return nil }
func (noPreflightDriver) TargetArchitecture(context.Context, string) (string, error) {
	return "", nil
}
func (noPreflightDriver) DeleteDevContainer(context.Context, string) error { return nil }
func (noPreflightDriver) StartDevContainer(context.Context, string) error  { return nil }
func (noPreflightDriver) StopDevContainer(context.Context, string) error   { return nil }
func (noPreflightDriver) GetDevContainerLogs(context.Context, string, io.Writer, io.Writer) error {
	return nil
}

func TestDriverPreflightNoOpWithoutCapability(t *testing.T) {
	if err := DriverPreflight(
		context.Background(),
		noPreflightDriver{},
		PreflightOptions{},
	); err != nil {
		t.Fatalf("DriverPreflight on non-Preflighter driver = %v, want nil", err)
	}
}

// preflightSpy is a Preflighter that records the options it received.
type preflightSpy struct {
	noPreflightDriver
	got PreflightOptions
}

func (s *preflightSpy) Preflight(_ context.Context, opts PreflightOptions) error {
	s.got = opts
	return nil
}

func TestDriverPreflightForwardsOptions(t *testing.T) {
	spy := &preflightSpy{}
	if err := DriverPreflight(
		context.Background(),
		spy,
		PreflightOptions{DisableAutoStart: true},
	); err != nil {
		t.Fatalf("DriverPreflight = %v, want nil", err)
	}
	if !spy.got.DisableAutoStart {
		t.Error("DriverPreflight did not forward DisableAutoStart to the driver")
	}
}
