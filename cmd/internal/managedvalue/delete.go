package managedvalue

import (
	"errors"
	"fmt"
	"slices"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/secrets"
)

type BindingKind uint8

const (
	EnvBinding BindingKind = iota
	SecretBinding
)

type DeleteRequest struct {
	Config  *config.Config
	Store   interface{ Delete(string, string) error }
	Context string
	Name    string
	Binding BindingKind
	Save    func(*config.Config) error
}

type ConsistencyError struct {
	Context  string
	Name     string
	Binding  BindingKind
	Phase    string
	Cause    error
	Rollback error
}

func (e *ConsistencyError) Error() string {
	kind := e.bindingName()
	switch e.Phase {
	case "store-indeterminate":
		return fmt.Sprintf(
			"could not complete deletion of %s %q in context %q: "+
				"the store outcome is indeterminate and the context attachment is detached; "+
				"restore backend access, verify the value, then attach it again or retry deletion",
			kind, e.Name, e.Context,
		)
	case "config-rollback":
		return fmt.Sprintf(
			"could not restore the %s attachment for %q in context %q: "+
				"the stored value was preserved but config.yaml remains detached; "+
				"repair config persistence, then attach the value again or retry deletion",
			kind, e.Name, e.Context,
		)
	default:
		return fmt.Sprintf(
			"could not complete deletion of %s %q in context %q",
			kind, e.Name, e.Context,
		)
	}
}

func (e *ConsistencyError) Unwrap() []error {
	return []error{e.Cause, e.Rollback}
}

func (e *ConsistencyError) bindingName() string {
	if e.Binding == SecretBinding {
		return "secret"
	}
	return "environment variable"
}

type removedBinding struct {
	attached bool
	index    int
}

func Delete(request DeleteRequest) error {
	if request.Config == nil || request.Store == nil {
		return errors.New("managed value delete requires config and store")
	}
	if request.Save == nil {
		request.Save = config.SaveConfig
	}

	ctxConfig := request.Config.Contexts[request.Context]
	removed, err := persistDetachedBinding(request, ctxConfig)
	if err != nil {
		return err
	}

	if err := request.Store.Delete(request.Context, request.Name); err != nil {
		return compensateAttachment(request, ctxConfig, removed, err)
	}
	return nil
}

func persistDetachedBinding(
	request DeleteRequest,
	ctxConfig *config.ContextConfig,
) (removedBinding, error) {
	removed := removeBinding(ctxConfig, request.Binding, request.Name)
	if !removed.attached {
		return removed, nil
	}
	if err := request.Save(request.Config); err != nil {
		restoreBinding(ctxConfig, request.Binding, request.Name, removed)
		return removedBinding{}, err
	}
	return removed, nil
}

func compensateAttachment(
	request DeleteRequest,
	ctxConfig *config.ContextConfig,
	removed removedBinding,
	deleteErr error,
) error {
	if !removed.attached {
		return deleteErr
	}
	if errors.Is(deleteErr, secrets.ErrStateIndeterminate) {
		return &ConsistencyError{
			Context: request.Context,
			Name:    request.Name,
			Binding: request.Binding,
			Phase:   "store-indeterminate",
			Cause:   deleteErr,
		}
	}

	restoreBinding(ctxConfig, request.Binding, request.Name, removed)
	return persistRestoredBinding(request, deleteErr)
}

func persistRestoredBinding(request DeleteRequest, deleteErr error) error {
	if err := request.Save(request.Config); err != nil {
		return &ConsistencyError{
			Context:  request.Context,
			Name:     request.Name,
			Binding:  request.Binding,
			Phase:    "config-rollback",
			Cause:    deleteErr,
			Rollback: err,
		}
	}
	return deleteErr
}

func removeBinding(ctxConfig *config.ContextConfig, kind BindingKind, name string) removedBinding {
	if ctxConfig == nil {
		return removedBinding{}
	}
	bindings := bindingsFor(ctxConfig, kind)
	index := slices.Index(*bindings, name)
	if index < 0 {
		return removedBinding{}
	}
	*bindings = slices.Delete(*bindings, index, index+1)
	return removedBinding{attached: true, index: index}
}

func restoreBinding(
	ctxConfig *config.ContextConfig,
	kind BindingKind,
	name string,
	removed removedBinding,
) {
	if ctxConfig == nil || !removed.attached {
		return
	}
	bindings := bindingsFor(ctxConfig, kind)
	if slices.Contains(*bindings, name) {
		return
	}
	index := min(removed.index, len(*bindings))
	*bindings = append(*bindings, "")
	copy((*bindings)[index+1:], (*bindings)[index:])
	(*bindings)[index] = name
}

func bindingsFor(ctxConfig *config.ContextConfig, kind BindingKind) *[]string {
	if kind == SecretBinding {
		return &ctxConfig.Secrets
	}
	return &ctxConfig.EnvVars
}
