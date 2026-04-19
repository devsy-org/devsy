package platform

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/devsy-org/devsy/pkg/http"
	"github.com/devsy-org/devsy/pkg/provider"
)

type VersionObject struct {
	// Version is the server version
	Version string `json:"version,omitempty"`

	// Version is the remote devsy version
	DevsyVersion string `json:"devsyVersion,omitempty"`
}

func GetProInstanceDevsyVersion(proInstance *provider.ProInstance) (string, error) {
	url := "https://" + proInstance.Host
	return GetDevsyVersion(url)
}

func GetPlatformVersion(url string) (*VersionObject, error) {
	resp, err := http.GetHTTPClient().Get(url + "/version")
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", url, err)
	} else if resp.StatusCode != 200 {
		out, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get %s: %s (Status: %d)", url, string(out), resp.StatusCode)
	}

	versionRaw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}

	version := &VersionObject{}
	err = json.Unmarshal(versionRaw, version)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", url, err)
	}

	return version, nil
}

func GetDevsyVersion(url string) (string, error) {
	version, err := GetPlatformVersion(url)
	if err != nil {
		return "", err
	}
	if version.DevsyVersion == "" {
		return "", fmt.Errorf(
			"unexpected version '%s', please use --version to define a provider version",
			version.DevsyVersion,
		)
	}

	// make sure it starts with a v
	if !strings.HasPrefix(version.DevsyVersion, "v") {
		version.DevsyVersion = "v" + version.DevsyVersion
	}

	return version.DevsyVersion, nil
}
