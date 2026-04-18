package workspace

import (
	"os"

	"github.com/devsy-org/devsy/pkg/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/skevetter/log"
)

func ListProInstances(
	devsyConfig *config.Config,
	log log.Logger,
) ([]*provider2.ProInstance, error) {
	proInstanceDir, err := provider2.GetProInstancesDir(devsyConfig.DefaultContext)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(proInstanceDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	retProInstances := []*provider2.ProInstance{}
	for _, entry := range entries {
		proInstanceConfig, err := provider2.LoadProInstanceConfig(
			devsyConfig.DefaultContext,
			entry.Name(),
		)
		if err != nil {
			log.Warnf("could not load pro instance: instance=%s, error=%v", entry.Name(), err)
			continue
		}

		retProInstances = append(retProInstances, proInstanceConfig)
	}

	return retProInstances, nil
}

func FindProviderProInstance(
	proInstances []*provider2.ProInstance,
	providerName string,
) (*provider2.ProInstance, bool) {
	for _, instance := range proInstances {
		if instance.Provider == providerName {
			return instance, true
		}
	}

	return nil, false
}
