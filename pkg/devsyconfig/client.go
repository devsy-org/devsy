package devsyconfig

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/devsy-org/devsy/pkg/credentials"
	"github.com/devsy-org/devsy/pkg/platform/client"
)

func GetDevsyConfig(port int) (*client.Config, error) {
	out, err := credentials.PostWithRetry(
		port,
		"devsy-platform-credentials",
		http.NoBody,
	)
	if err != nil {
		return nil, err
	}

	configResponse := &DevsyConfigResponse{}
	if err := json.Unmarshal(out, configResponse); err != nil {
		return nil, fmt.Errorf("decode devsy config %s: %w", string(out), err)
	}

	return configResponse.DevsyConfig, nil
}
