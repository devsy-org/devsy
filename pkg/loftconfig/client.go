package devsyconfig

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/devsy-org/devsy/pkg/credentials"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform/client"
)

func GetDevsyConfig(context, provider string, port int) (*client.Config, error) {
	request := &DevsyConfigRequest{
		Context:  context,
		Provider: provider,
	}

	rawJson, err := json.Marshal(request)
	if err != nil {
		log.Errorf("Error parsing request: %w", err)
		return nil, err
	}

	configResponse := &DevsyConfigResponse{}
	out, err := credentials.PostWithRetry(
		port,
		"loft-platform-credentials",
		bytes.NewReader(rawJson),
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
