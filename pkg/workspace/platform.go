package workspace

import (
	"os"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

func ListPlatformInstances(
	devsyConfig *config.Config,
) ([]*provider2.ProInstance, error) {
	platformInstanceDir, err := provider2.GetProInstancesDir(devsyConfig.DefaultContext)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(platformInstanceDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	retPlatformInstances := []*provider2.ProInstance{}
	for _, entry := range entries {
		platformInstanceConfig, err := provider2.LoadProInstanceConfig(
			devsyConfig.DefaultContext,
			entry.Name(),
		)
		if err != nil {
			log.Warnf("could not load platform instance: instance=%s, error=%v", entry.Name(), err)
			continue
		}

		retPlatformInstances = append(retPlatformInstances, platformInstanceConfig)
	}

	return retPlatformInstances, nil
}

func FindProviderPlatformInstance(
	platformInstances []*provider2.ProInstance,
	providerName string,
) (*provider2.ProInstance, bool) {
	for _, instance := range platformInstances {
		if instance.Provider == providerName {
			return instance, true
		}
	}

	return nil, false
}
