package devsyconfig

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/devsy-org/devsy/pkg/credentials"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/log"
)

func GetDevsyConfig(context, provider string, port int, logger log.Logger) (*client.Config, error) {
	request := &DevsyConfigRequest{
		Context:  context,
		Provider: provider,
	}

	rawJson, err := json.Marshal(request)
	if err != nil {
		logger.Errorf("Error parsing request: %w", err)
		return nil, err
	}

	configResponse := &DevsyConfigResponse{}
	out, err := credentials.PostWithRetry(
		port,
		"loft-platform-credentials",
		bytes.NewReader(rawJson),
		logger,
	)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(out, configResponse)
	if err != nil {
		return nil, fmt.Errorf("decode loft config %s: %w", string(out), err)
	}

	return configResponse.DevsyConfig, nil
}
