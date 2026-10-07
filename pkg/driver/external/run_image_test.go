package external

import (
	"math"
	"testing"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/suite"
	"google.golang.org/protobuf/proto"
)

const (
	imageRunImage           = "image"
	imageRunEntrypoint      = "/bin/sh"
	imageRunMapping         = "0:1000:1"
	imageRunWorkspaceTarget = "/workspace"
	imageRunVolume          = "data"
	imageRunTmpfsTarget     = "/tmp"
)

type ImageRunSuite struct{ suite.Suite }

func TestImageRunSuite(t *testing.T) { suite.Run(t, new(ImageRunSuite)) }

//nolint:funlen // Complete wire mapping and ownership assertions.
func (s *ImageRunSuite) TestResolvedIntentAndOwnership() {
	yes, no := true, false
	params := &driver.RunImageDevContainerParams{
		WorkspaceID:          fixtureWorkspace,
		IDE:                  "not-forwarded",
		LocalWorkspaceFolder: "/not-forwarded",
		Options: &driver.RunOptions{
			UID:        "host-uid",
			Image:      imageRunImage,
			ImageBuilt: true,
			User:       fixtureProcessUser,
			RemoteUser: fixtureRemoteUser,
			Dockerless: true,
			Entrypoint: imageRunEntrypoint,
			Cmd:        []string{"", "literal $arg"},
			Env:        map[string]string{"TOKEN": "secret"},
			Labels: []string{
				"name=value",
			},
			CapAdd:      []string{"SYS_PTRACE"},
			SecurityOpt: []string{"seccomp=unconfined"},
			Privileged:  &no,
			Init:        &yes,
			Userns:      "keep-id",
			UidMap:      []string{imageRunMapping},
			GidMap:      []string{imageRunMapping},
			Platform:    "linux/arm64",
			WorkspaceMount: &config.Mount{
				Type:   "bind",
				Source: "/host",
				Target: imageRunWorkspaceTarget,
				Other:  []string{"readonly", "consistency=cached"},
			},
			Mounts: []*config.Mount{
				{Type: driver.MountTypeVolume, Source: imageRunVolume, Target: "/data"},
				{
					Type:   driver.MountTypeTmpfs,
					Target: imageRunTmpfsTarget,
					Other:  []string{"tmpfs-size=4096"},
				},
			},
		},
		ParsedConfig: &config.DevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{
				HostRequirements: &config.HostRequirements{
					CPUs:    2,
					Memory:  "2gb",
					Storage: "10gb",
					GPU:     &config.GPURequirement{Value: "optional"},
				},
			},
		},
	}
	got, err := runImageRequest(params)
	s.Require().NoError(err)
	want := &runtimev1.RunImageRequest{
		WorkspaceId:       fixtureWorkspace,
		Image:             imageRunImage,
		ImageBuiltLocally: true,
		User:              fixtureProcessUser,
		RemoteUser:        fixtureRemoteUser,
		Dockerless:        true,
		Entrypoint:        imageRunEntrypoint,
		Args:              []string{"", "literal $arg"},
		Environment: map[string]string{
			"TOKEN": "secret",
		},
		Labels:      []string{"name=value"},
		CapAdd:      []string{"SYS_PTRACE"},
		SecurityOpt: []string{"seccomp=unconfined"},
		Privileged:  &no,
		Init:        &yes,
		Userns:      "keep-id",
		UidMap:      []string{imageRunMapping},
		GidMap:      []string{imageRunMapping},
		Platform:    "linux/arm64",
		WorkspaceMount: &runtimev1.Mount{
			Type:     runtimev1.MountType_MOUNT_TYPE_BIND,
			Source:   "/host",
			Target:   imageRunWorkspaceTarget,
			ReadOnly: true,
			Options:  []string{"readonly", "consistency=cached"},
		},
		Mounts: []*runtimev1.Mount{
			{Type: runtimev1.MountType_MOUNT_TYPE_VOLUME, Source: imageRunVolume, Target: "/data"},
			{
				Type:    runtimev1.MountType_MOUNT_TYPE_TMPFS,
				Target:  imageRunTmpfsTarget,
				Options: []string{"tmpfs-size=4096"},
			},
		},
		HostRequirements: &runtimev1.HostRequirements{
			Cpus:         2,
			MemoryBytes:  2 << 30,
			StorageBytes: 10 << 30,
			Gpu: &runtimev1.GpuRequirement{
				Value: runtimev1.GpuRequirementValue_GPU_OPTIONAL,
			},
		},
	}
	s.True(proto.Equal(want, got))
	want = proto.Clone(want).(*runtimev1.RunImageRequest)
	params.Options.Env["TOKEN"] = "changed-env"
	params.Options.Cmd[0] = "changed-arg"
	params.Options.WorkspaceMount.Other[0] = "changed-mount"
	yes = false
	s.True(proto.Equal(want, got), "request must own mutable inputs")
	s.True(*got.Init)
	s.False(*got.Privileged)
}

//nolint:funlen // Malformed creation-intent table and numeric boundaries.
func (s *ImageRunSuite) TestInvalidIntentRejectedWithoutLaunchingRuntime() {
	h := &Host{
		info: &runtimev1.InfoResponse{
			Capabilities: &runtimev1.Capabilities{
				MountTypes: []runtimev1.MountType{runtimev1.MountType_MOUNT_TYPE_BIND},
			},
		},
	}
	for _, tc := range []struct {
		name   string
		mutate func(*driver.RunImageDevContainerParams)
	}{
		{"missing options", func(p *driver.RunImageDevContainerParams) { p.Options = nil }},
		{"missing workspace", func(p *driver.RunImageDevContainerParams) { p.WorkspaceID = "" }},
		{"missing image", func(p *driver.RunImageDevContainerParams) { p.Options.Image = "" }},
		{"unknown mount", func(p *driver.RunImageDevContainerParams) {
			p.Options.Mounts = []*config.Mount{{Type: "unknown"}}
		}},
		{"missing bind source", func(p *driver.RunImageDevContainerParams) {
			p.Options.WorkspaceMount = &config.Mount{Type: driver.MountTypeBind, Target: imageRunWorkspaceTarget}
		}},
		{"missing mount target", func(p *driver.RunImageDevContainerParams) {
			p.Options.Mounts = []*config.Mount{{Type: driver.MountTypeVolume, Source: imageRunVolume}}
		}},
		{"nil mount", func(p *driver.RunImageDevContainerParams) { p.Options.Mounts = []*config.Mount{nil} }},
		{"unsupported mount", func(p *driver.RunImageDevContainerParams) {
			p.Options.Mounts = []*config.Mount{{Type: driver.MountTypeTmpfs, Target: imageRunTmpfsTarget}}
		}},
		{"external volume", func(p *driver.RunImageDevContainerParams) {
			p.Options.Mounts = []*config.Mount{{Type: driver.MountTypeVolume, External: true}}
		}},
		{"raw run args", func(p *driver.RunImageDevContainerParams) { p.ParsedConfig.RunArgs = []string{"--network=host"} }},
		{"app port", func(p *driver.RunImageDevContainerParams) { p.ParsedConfig.AppPort = []string{"8080:80"} }},
		{"negative cpu", func(p *driver.RunImageDevContainerParams) { p.ParsedConfig.HostRequirements.CPUs = -1 }},
		{"invalid memory", func(p *driver.RunImageDevContainerParams) { p.ParsedConfig.HostRequirements.Memory = "-1gb" }},
		{"overflow storage", func(p *driver.RunImageDevContainerParams) {
			p.ParsedConfig.HostRequirements.Storage = "18446744073709551615tb"
		}},
		{"gpu detail", func(p *driver.RunImageDevContainerParams) {
			p.ParsedConfig.HostRequirements.GPU = &config.GPURequirement{Value: "true", Cores: 8}
		}},
		{"invalid gpu", func(p *driver.RunImageDevContainerParams) {
			p.ParsedConfig.HostRequirements.GPU = &config.GPURequirement{Value: "invalid"}
		}},
	} {
		s.Run(tc.name, func() {
			p := &driver.RunImageDevContainerParams{
				WorkspaceID: fixtureWorkspace,
				Options:     &driver.RunOptions{Image: imageRunImage},
				ParsedConfig: &config.DevContainerConfig{
					DevContainerConfigBase: config.DevContainerConfigBase{
						HostRequirements: &config.HostRequirements{},
					},
				},
			}
			tc.mutate(p)
			s.Error(h.ValidateRunImageDevContainer(p))
		})
	}
	s.Error(h.ValidateRunImageDevContainer(nil))
	for _, value := range []string{"0", "512mb", "1GB", " 2 gb ", "18446744073709551615"} {
		_, err := requirementBytes(value)
		s.NoError(err)
	}
	got, err := requirementBytes("18446744073709551615")
	s.NoError(err)
	s.Equal(uint64(math.MaxUint64), got)
	_, err = convertHostRequirements(&config.HostRequirements{CPUs: int(math.MaxInt32) + 1})
	s.Error(err)
}

func (s *ImageRunSuite) TestNegotiatedCapabilities() {
	for _, mode := range []runtimev1.RecreateMode{
		runtimev1.RecreateMode_RECREATE_MODE_DELETE,
		runtimev1.RecreateMode_RECREATE_MODE_STOP,
	} {
		h := &Host{
			info: &runtimev1.InfoResponse{
				Capabilities: &runtimev1.Capabilities{
					MountTypes: []runtimev1.MountType{
						runtimev1.MountType_MOUNT_TYPE_BIND,
					},
					RequiresMountStreaming: true,
					RequiresWorkspaceChown: true,
					RecreateMode:           mode,
				},
			},
		}
		s.True(h.SupportsMountType("bind"))
		s.False(h.SupportsMountType("volume"))
		s.False(h.SupportsMountType("unknown"))
		s.True(driver.DriverRequiresMountStreaming(h))
		s.True(driver.DriverRequiresWorkspaceChown(h))
		want := driver.RecreateStop
		if mode == runtimev1.RecreateMode_RECREATE_MODE_DELETE {
			want = driver.RecreateDelete
		}
		s.Equal(want, driver.DriverRecreateMode(h))
	}
}
