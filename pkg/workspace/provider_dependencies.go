package workspace

import (
	"context"
	"errors"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/dependencies"
	"github.com/devsy-org/devsy/pkg/provider"
)

// ProviderDependencySurveyRequest contains already-loaded, unexpanded provider and effective invocation snapshots.
// Callers must not mutate its inputs during the call; defaults and environment precedence are already resolved.
type ProviderDependencySurveyRequest struct {
	Provider provider.ProviderConfig
	Options  map[string]config.OptionValue
	// OptionOrigins may assert explicit_env only when the caller established that origin during resolution.
	OptionOrigins    map[string]dependencies.LookupOrigin
	Environment      map[string]string
	WorkingDirectory string
	Target           dependencies.SurveyTarget
	Host             dependencies.SurveyHost
}

func SurveyProviderDependencies(
	ctx context.Context,
	request ProviderDependencySurveyRequest,
) (dependencies.SurveyResult, error) {
	if err := ctx.Err(); err != nil {
		return dependencies.SurveyResult{}, err
	}
	catalog, err := dependencies.LoadCatalog()
	if err != nil {
		return dependencies.SurveyResult{}, errors.New("cannot load provider dependency catalog")
	}
	environment, err := dependencies.NewLookupEnvironment(
		request.Environment,
		request.WorkingDirectory,
	)
	if err != nil {
		return dependencies.SurveyResult{}, err
	}
	contract, err := providerDependencyContract(catalog, request.Provider)
	if err != nil {
		return dependencies.SurveyResult{}, err
	}
	return catalog.Survey(ctx, dependencies.SurveyRequest{
		Target: request.Target, Host: request.Host, Contract: contract,
		Environment: environment, Options: providerDependencyOptions(request),
	})
}

func providerDependencyOptions(
	request ProviderDependencySurveyRequest,
) map[string]dependencies.SurveyOption {
	options := make(map[string]dependencies.SurveyOption, len(request.Options))
	for name, value := range request.Options {
		options[name] = dependencies.SurveyOption{
			Value:        value.Value,
			UserProvided: value.UserProvided,
			Origin:       request.OptionOrigins[name],
		}
	}
	return options
}
