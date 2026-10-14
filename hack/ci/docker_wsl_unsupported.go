//go:build !windows

package main

// unsupportedHost fails every operation: the docker-wsl fixture needs native
// Windows with wsl.exe, ssh.exe and icacls.exe.
type unsupportedHost struct{}

func newHostPlatform(*Dependencies) hostPlatform { return unsupportedHost{} }

func (unsupportedHost) LookPath(file string) (string, error) {
	return "", unsupportedProfileError(
		"docker-wsl requires native Windows; %s is unavailable",
		file,
	)
}

func (unsupportedHost) StartDetached(string, []string) error {
	return unsupportedProfileError("docker-wsl requires native Windows")
}
