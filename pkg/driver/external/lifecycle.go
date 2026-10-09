package external

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (h *Host) Preflight(ctx context.Context, options driver.PreflightOptions) error {
	return h.call(ctx, "Preflight", func(client runtimev1.RuntimeDriverClient) error {
		_, err := client.Preflight(
			ctx,
			&runtimev1.PreflightRequest{DisableAutoStart: options.DisableAutoStart},
		)
		return err
	})
}

func (h *Host) ProvisioningPreflight(ctx context.Context) error {
	if !h.info.Capabilities.ProvisioningPreflight {
		return ctx.Err()
	}
	return h.call(ctx, "ProvisioningPreflight", func(client runtimev1.RuntimeDriverClient) error {
		_, err := client.ProvisioningPreflight(ctx, &runtimev1.ProvisioningPreflightRequest{})
		return err
	})
}

func (h *Host) ReusePreflight(ctx context.Context, workspaceID, remoteUser string) error {
	if !h.SupportsReusePreflight() {
		return ctx.Err()
	}
	if workspaceID == "" || remoteUser == "" {
		return errors.New("runtime reuse preflight requires workspace ID and remote user")
	}
	return h.forWorkspace(workspaceID, nil).
		call(ctx, "ReusePreflight", func(client runtimev1.RuntimeDriverClient) error {
			_, err := client.ReusePreflight(ctx, &runtimev1.ReusePreflightRequest{
				WorkspaceId: workspaceID, RemoteUser: remoteUser,
			})
			return err
		})
}

func (h *Host) FindDevContainer(
	ctx context.Context,
	workspaceID string,
) (*config.ContainerDetails, error) {
	if workspaceID == "" {
		return nil, errors.New("workspace ID is required")
	}
	var container *config.ContainerDetails
	operation := h.forWorkspace(workspaceID, nil)
	err := operation.call(ctx, "Find", func(client runtimev1.RuntimeDriverClient) error {
		response, err := client.Find(ctx, &runtimev1.FindRequest{WorkspaceId: workspaceID})
		if err != nil {
			return err
		}
		if response == nil {
			return errors.New("runtime Find response is missing")
		}
		if !response.Found {
			return nil
		}
		container, err = convertContainer(response.Container)
		if container != nil {
			container.State.Error = operation.redactor.Redact(container.State.Error)
		}
		return err
	})
	return container, err
}

func (h *Host) TargetArchitecture(ctx context.Context, workspaceID string) (string, error) {
	if workspaceID == "" {
		return "", errors.New("workspace ID is required")
	}
	var architecture string
	err := h.forWorkspace(workspaceID, nil).
		call(ctx, "TargetArchitecture", func(client runtimev1.RuntimeDriverClient) error {
			response, err := client.TargetArchitecture(
				ctx,
				&runtimev1.TargetArchitectureRequest{WorkspaceId: workspaceID},
			)
			if err != nil {
				return err
			}
			architecture = response.GetArchitecture()
			if architecture != "amd64" && architecture != "arm64" {
				return fmt.Errorf("invalid runtime target architecture %q", architecture)
			}
			return nil
		})
	return architecture, err
}

// RunImage accepts resolved protocol intent and validates negotiated mount support.
func (h *Host) RunImage(ctx context.Context, request *runtimev1.RunImageRequest) error {
	if err := h.validateRunImage(request); err != nil {
		return err
	}
	operation := h.forWorkspace(request.WorkspaceId, request.Environment)
	return operation.call(ctx, "RunImage", func(client runtimev1.RuntimeDriverClient) error {
		_, err := client.RunImage(ctx, request)
		return err
	})
}

func (h *Host) validateRunImage(request *runtimev1.RunImageRequest) error {
	if request == nil || request.WorkspaceId == "" || request.Image == "" {
		return errors.New("runtime RunImage requires workspace ID and image")
	}
	mounts := slices.Clone(request.Mounts)
	if request.WorkspaceMount != nil {
		mounts = append(mounts, request.WorkspaceMount)
	}
	for _, mount := range mounts {
		if mount == nil {
			return errors.New("runtime RunImage mount is missing")
		}
		if !slices.Contains(h.info.Capabilities.MountTypes, mount.Type) {
			return fmt.Errorf("runtime does not support requested mount type %s", mount.Type)
		}
	}
	return nil
}

func (h *Host) StartDevContainer(ctx context.Context, workspaceID string) error {
	if workspaceID == "" {
		return errors.New("workspace ID is required")
	}
	return h.forWorkspace(workspaceID, nil).
		call(ctx, "Start", func(client runtimev1.RuntimeDriverClient) error {
			_, err := client.Start(ctx, &runtimev1.StartRequest{WorkspaceId: workspaceID})
			return err
		})
}

func (h *Host) StopDevContainer(ctx context.Context, workspaceID string) error {
	if workspaceID == "" {
		return errors.New("workspace ID is required")
	}
	return h.forWorkspace(workspaceID, nil).
		call(ctx, "Stop", func(client runtimev1.RuntimeDriverClient) error {
			_, err := client.Stop(ctx, &runtimev1.StopRequest{WorkspaceId: workspaceID})
			return err
		})
}

func (h *Host) DeleteDevContainer(ctx context.Context, workspaceID string) error {
	if workspaceID == "" {
		return errors.New("workspace ID is required")
	}
	return h.forWorkspace(workspaceID, nil).
		call(ctx, "Delete", func(client runtimev1.RuntimeDriverClient) error {
			_, err := client.Delete(ctx, &runtimev1.DeleteRequest{WorkspaceId: workspaceID})
			if status.Code(err) == codes.NotFound {
				return nil
			}
			return err
		})
}

func convertContainer(source *runtimev1.ContainerDetails) (*config.ContainerDetails, error) {
	if source == nil || source.Id == "" || source.State == nil {
		return nil, errors.New("runtime Find requires container ID and state")
	}
	state, err := convertState(source.State.Status)
	if err != nil {
		return nil, err
	}
	result := &config.ContainerDetails{
		ID: source.Id, Created: source.CreatedAt,
		State: config.ContainerDetailsState{
			Status: state, StartedAt: source.State.StartedAt,
			ExitCode: int(source.State.ExitCode), Error: source.State.Error,
		},
	}
	if source.Config != nil {
		result.Config = config.ContainerDetailsConfig{
			Labels:     source.Config.Labels,
			WorkingDir: source.Config.WorkingDir, User: source.Config.User,
		}
	}
	result.Mounts, err = convertMounts(source.Mounts)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func convertMounts(source []*runtimev1.ContainerMount) ([]config.ContainerMount, error) {
	result := make([]config.ContainerMount, 0, len(source))
	for _, mount := range source {
		if mount == nil {
			return nil, errors.New("runtime container mount is missing")
		}
		result = append(
			result,
			config.ContainerMount{
				Type:        mount.Type,
				Source:      mount.Source,
				Destination: mount.Destination,
			},
		)
	}
	return result, nil
}

func convertState(status string) (config.ContainerStatus, error) {
	switch config.ToContainerStatus(status) {
	case config.ContainerStatusRunning:
		return config.ContainerStatusRunning, nil
	case "stopped":
		return config.ContainerStatusExited, nil
	default:
		return "", fmt.Errorf("invalid runtime container state %q", status)
	}
}
