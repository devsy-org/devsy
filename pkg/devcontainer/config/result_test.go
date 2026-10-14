package config

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/clierr"
)

const (
	errBoom             = "boom"
	testVolumeMountType = "volume"
)

func TestResultErrRecoveryAvailable(t *testing.T) {
	err := (&Result{Error: "build image: boom", RecoveryAvailable: true}).Err()
	if err == nil || err.Error() != "build image: boom" {
		t.Fatalf("Err() = %v, want message preserved", err)
	}
	if !errors.Is(err, clierr.ErrBuildFailedRecoverable) {
		t.Fatal("recovery-available error must classify as recoverable")
	}

	plain := (&Result{Error: errBoom}).Err()
	if errors.Is(plain, clierr.ErrBuildFailedRecoverable) {
		t.Fatal("plain error must not be recoverable")
	}
}

func TestResultErr(t *testing.T) {
	tests := []struct {
		name    string
		result  *Result
		wantErr bool
		wantMsg string
	}{
		{name: "nil result", result: nil, wantErr: false},
		{name: "no error", result: &Result{}, wantErr: false},
		{name: "with error", result: &Result{Error: errBoom}, wantErr: true, wantMsg: errBoom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.result.Err()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if err.Error() != tt.wantMsg {
					t.Fatalf("expected %q, got %q", tt.wantMsg, err.Error())
				}
			} else if err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
		})
	}
}

func TestGetRemoteUser(t *testing.T) {
	for _, tt := range getRemoteUserCases() {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetRemoteUser(tt.result); got != tt.want {
				t.Errorf("GetRemoteUser() = %q, want %q", got, tt.want)
			}
		})
	}
}

type getRemoteUserCase struct {
	name   string
	result *Result
	want   string
}

func getRemoteUserCases() []getRemoteUserCase {
	return append(remoteUserRootFallbackCases(), remoteUserPrecedenceCases()...)
}

func remoteUserRootFallbackCases() []getRemoteUserCase {
	return []getRemoteUserCase{
		{
			name:   "nil result falls back to root",
			result: nil,
			want:   testUserRoot,
		},
		{
			name: "no user sources falls back to root",
			result: &Result{
				MergedConfig: &MergedDevContainerConfig{},
				ContainerDetails: &ContainerDetails{
					Config: ContainerDetailsConfig{},
				},
			},
			want: testUserRoot,
		},
	}
}

func remoteUserPrecedenceCases() []getRemoteUserCase {
	return []getRemoteUserCase{
		{
			name: "remoteUser from config wins",
			result: &Result{
				MergedConfig: &MergedDevContainerConfig{
					DevContainerConfigBase: DevContainerConfigBase{
						RemoteUser: "cfg-user",
					},
					NonComposeBase: NonComposeBase{ContainerUser: testContainerUser},
				},
				ContainerDetails: &ContainerDetails{
					Config: ContainerDetailsConfig{User: testInspectUser},
				},
			},
			want: "cfg-user",
		},
		{
			name: "devsy.user label beats docker inspect user",
			result: &Result{
				ContainerDetails: &ContainerDetails{
					Config: ContainerDetailsConfig{
						User:   testInspectUser,
						Labels: map[string]string{UserLabel: testLabelUser},
					},
				},
			},
			want: testLabelUser,
		},
		{
			name: "containerUser from config beats devsy.user label",
			result: &Result{
				MergedConfig: &MergedDevContainerConfig{
					NonComposeBase: NonComposeBase{ContainerUser: testContainerUser},
				},
				ContainerDetails: &ContainerDetails{
					Config: ContainerDetailsConfig{
						User:   testInspectUser,
						Labels: map[string]string{UserLabel: testLabelUser},
					},
				},
			},
			want: testContainerUser,
		},
		{
			name: "containerUser beats docker inspect user",
			result: &Result{
				MergedConfig: &MergedDevContainerConfig{
					NonComposeBase: NonComposeBase{ContainerUser: testContainerUser},
				},
				ContainerDetails: &ContainerDetails{
					Config: ContainerDetailsConfig{User: testInspectUser},
				},
			},
			want: testContainerUser,
		},
	}
}

const (
	testContainerUser = "container-user"
	testInspectUser   = "inspect-user"
	testLabelUser     = "label-user"
)

func TestWorkspaceMountIncompleteResults(t *testing.T) {
	for _, result := range []*Result{nil, {}, {MergedConfig: &MergedDevContainerConfig{}}} {
		if GetWorkspaceMount(result) != nil {
			t.Fatal("incomplete result invented a workspace mount")
		}
		if len(GetMounts(result)) != 0 {
			t.Fatal("incomplete result invented mounts")
		}
	}
	result := &Result{
		SubstitutionContext: &SubstitutionContext{
			WorkspaceMount: "type=volume,src=workspace,dst=/workspace",
		},
	}
	if GetWorkspaceMount(result).Type != testVolumeMountType {
		t.Fatal("workspace mount type lost")
	}
	if len(GetMounts(result)) != 1 {
		t.Fatal("workspace mount omitted")
	}
}

func TestGeneratedArtifactsCannotBeGrantedBySerializedResult(t *testing.T) {
	var result Result
	err := json.Unmarshal(
		[]byte(
			`{"GeneratedBuildContext":"/private","GeneratedBuildArtifacts":[{"Path":"/private/secret","SHA256":"trusted"}]}`,
		),
		&result,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.GeneratedBuildContext != "" || len(result.GeneratedBuildArtifacts) > 0 {
		t.Fatal("remote result granted generated artifact authority")
	}
}
