//go:build !windows

package dockerwsl

import "github.com/devsy-org/devsy/e2e/framework/runenv"

// unsupportedHost fails every operation: the docker-wsl fixture needs native
// Windows with wsl.exe, ssh.exe and icacls.exe.
type unsupportedHost struct{}

func newHostPlatform(*runenv.Dependencies) hostPlatform { return unsupportedHost{} }

func (unsupportedHost) LookPath(file string) (string, error) {
	return "", runenv.UnsupportedProfileError(
		"docker-wsl requires native Windows; %s is unavailable",
		file,
	)
}

func (unsupportedHost) StartDetached(string, []string) error {
	return runenv.UnsupportedProfileError("docker-wsl requires native Windows")
}
