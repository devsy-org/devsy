package dependencies

import (
	"errors"
	"fmt"
)

func (d catalogDocument) validate() error {
	if d.SchemaVersion != 1 || d.ID != "devsy.dependencies.catalog.v1" {
		return errors.New("unsupported dependency catalog schema or identity")
	}
	return d.validateBindings()
}

func (d catalogDocument) validateBindings() error {
	if d.ManagedStatus != "no_qualified_recipe" || len(d.Bindings) != 7 {
		return errors.New("incomplete or unsupported dependency catalog")
	}
	identities := make(map[string]bool)
	for _, binding := range d.Bindings {
		if identities[binding.ID] {
			return fmt.Errorf("duplicate binding %q", binding.ID)
		}
		identities[binding.ID] = true
	}
	return nil
}

func externalSource(id string) ProviderSource {
	return ProviderSource{
		Kind:       SourceGitHub,
		Repository: "github.com/devsy-org/devsy-provider-" + id,
	}
}
