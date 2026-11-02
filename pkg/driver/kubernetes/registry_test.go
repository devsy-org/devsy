package kubernetes

import "testing"

const (
	shouldReturnOfficialDockerRegistry = "should return official docker registry"
)

func TestGetRegistryFromImageName(t *testing.T) {
	type args struct {
		imageName string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: shouldReturnOfficialDockerRegistry,
			args: args{
				imageName: "docker.io/loftsh/devsy",
			},
			want:    OfficialDockerRegistry,
			wantErr: false,
		},
		{
			name: shouldReturnOfficialDockerRegistry,
			args: args{
				imageName: "hub.docker.com/loftsh/devsy",
			},
			want:    OfficialDockerRegistry,
			wantErr: false,
		},
		{
			name: shouldReturnOfficialDockerRegistry,
			args: args{
				imageName: "nginx:latest",
			},
			want:    OfficialDockerRegistry,
			wantErr: false,
		},
		{
			name: "should return github registry",
			args: args{
				imageName: "ghcr.io/devsyorg/devsy",
			},
			want:    "ghcr.io",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetRegistryFromImageName(tt.args.imageName)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetRegistryFromImageName() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetRegistryFromImageName() got = %v, want %v", got, tt.want)
			}
		})
	}
}
