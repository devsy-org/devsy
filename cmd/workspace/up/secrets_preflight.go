package up

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/devsy-org/devsy/cmd/internal/secretstore"
	"github.com/devsy-org/devsy/pkg/config"
	devcontainerconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/envstore"
	"github.com/devsy-org/devsy/pkg/secrets"
)

func (cmd *UpCmd) unlockOptions() secrets.StoreOptions {
	if cmd.secretOptions == nil {
		options := secretstore.Options()
		cmd.secretOptions = &options
	}
	return *cmd.secretOptions
}

type (
	unavailableValue       struct{ name, status string }
	unavailableValuesError struct {
		values []unavailableValue
		causes []error
	}
)

func (e *unavailableValuesError) Error() string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "Workspace requires %d unavailable values:\n", len(e.values))
	for _, value := range e.values {
		_, _ = fmt.Fprintf(&b, "\n  %s  %s", value.name, value.status)
	}
	b.WriteString("\n\nUnlock or repair these values and run the command again.")
	return b.String()
}
func (e *unavailableValuesError) Unwrap() []error { return e.causes }

// Preflight locally known requirements before workspace resolution can create
// remote resources. Project SOPS sources remain on the later discovery path.
func (cmd *UpCmd) preflightLocalValues(cfg *config.Config) error {
	refs, err := cmd.localPreflightRefs(cfg)
	if err != nil {
		return err
	}
	requests, err := cmd.preflightEnvRequests(cfg)
	if err != nil {
		return err
	}
	unavailable := &unavailableValuesError{}
	if err := preflightEnvironmentValues(cfg, requests, unavailable); err != nil {
		return err
	}
	if err := cmd.preflightSecretValues(cfg, refs, unavailable); err != nil {
		return err
	}
	if len(unavailable.values) > 0 {
		return unavailable
	}
	return nil
}

func (cmd *UpCmd) localPreflightRefs(cfg *config.Config) (map[string]secrets.SecretRef, error) {
	requests, err := collectSecretRequests(cmd.Secrets, cfg, nil)
	if err != nil {
		return nil, err
	}
	refs := make(map[string]secrets.SecretRef)
	for _, request := range requests {
		if err := addLocalPreflightRef(refs, request.ref); err != nil {
			return nil, err
		}
	}
	for _, name := range append(append([]string{}, cmd.BuildSecretNames...), cmd.GitTokenSecret) {
		if name == "" {
			continue
		}
		ref, err := secrets.ParseRef(name)
		if err != nil {
			return nil, err
		}
		if err := addLocalPreflightRef(refs, ref); err != nil {
			return nil, err
		}
	}
	return refs, nil
}

func addLocalPreflightRef(refs map[string]secrets.SecretRef, ref secrets.SecretRef) error {
	if ref.Source != secrets.LocalSourceName {
		return nil
	}
	if ref.Type != "" && ref.Type != secrets.LocalSourceName {
		return fmt.Errorf("local value %q must use the local source type", ref.Name)
	}
	refs[ref.Name] = ref
	return nil
}

func (cmd *UpCmd) preflightEnvRequests(cfg *config.Config) ([]envVarRequest, error) {
	requests, err := collectEnvVarRequests(cmd.EnvVars, cfg)
	if err != nil {
		return nil, err
	}
	assignments := append([]string{}, cmd.WorkspaceEnv...)
	for _, path := range cmd.WorkspaceEnvFile {
		parsed, err := devcontainerconfig.ParseKeyValueFile(path)
		if err != nil {
			return nil, err
		}
		assignments = append(assignments, parsed...)
	}
	base, err := indexWorkspaceEnv(assignments)
	if err != nil {
		return nil, err
	}
	return filterWorkspaceEnvRequests(base, requests)
}

func preflightEnvironmentValues(
	cfg *config.Config,
	requests []envVarRequest,
	unavailable *unavailableValuesError,
) error {
	if len(requests) == 0 {
		return nil
	}
	store, err := envstore.NewStoreForConfig(cfg)
	if err != nil {
		return err
	}
	for _, request := range requests {
		if _, err := store.Meta(cfg.DefaultContext, request.ref.Name); err != nil {
			unavailable.add(request.ref.Name, "environment missing or unavailable", err)
		}
	}
	return nil
}

func (cmd *UpCmd) preflightSecretValues(
	cfg *config.Config,
	refs map[string]secrets.SecretRef,
	unavailable *unavailableValuesError,
) error {
	if len(refs) == 0 {
		return nil
	}
	store, err := secrets.NewSecretStoreForConfig(cfg, cmd.unlockOptions())
	if err != nil {
		return err
	}
	// Get verifies required plaintext and permits command-owned TTY prompting.
	// Retain every failure, including its typed unlock result.
	for _, name := range sortedLocalNames(refs) {
		if _, err := store.Get(cfg.DefaultContext, name); err != nil {
			unavailable.add(name, preflightSecretStatus(err), err)
		}
	}
	return nil
}

func preflightSecretStatus(err error) string {
	switch {
	case errors.Is(err, secrets.ErrUnlockRequired), errors.Is(err, secrets.ErrUnlockFailed):
		return "locked"
	case errors.Is(err, secrets.ErrSecretNotFound):
		return "missing"
	case errors.Is(err, secrets.ErrBackendUnavailable):
		return "backend unavailable"
	case errors.Is(err, secrets.ErrStoreCorrupt):
		return "store corrupt"
	default:
		return "unknown"
	}
}

func (e *unavailableValuesError) add(name, state string, err error) {
	e.values = append(e.values, unavailableValue{name, state})
	e.causes = append(e.causes, err)
}

func sortedLocalNames(refs map[string]secrets.SecretRef) []string {
	names := make([]string, 0, len(refs))
	for name := range refs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
