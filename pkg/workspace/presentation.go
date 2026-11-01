package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/ssh"
	"github.com/gofrs/flock"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	PresentationFieldDisplayName  = "displayName"
	PresentationFieldSSHHostAlias = "sshHostAlias"
	PresentationFieldWorkspace    = "workspace"

	PresentationCodeInvalid  = "invalid"
	PresentationCodeConflict = "conflict"
	PresentationCodeStale    = "stale"
	PresentationCodeManaged  = "managed"
	PresentationCodeNotFound = "not-found"

	maxDisplayNameLength = 120
	presentationLockFile = "workspace-presentation.lock"
)

var sshHostAliasPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,46}[a-z0-9])?$`)

type PresentationRequest struct {
	DevsyConfig          *config.Config
	ID                   string
	ExpectedUID          string
	DisplayName          *string
	SSHHostAlias         *string
	ExpectedDisplayName  *string
	ExpectedSSHHostAlias *string
}

type PresentationResult struct {
	ID                   string `json:"id"`
	UID                  string `json:"uid"`
	Context              string `json:"context"`
	DisplayName          string `json:"displayName"`
	SSHHostAlias         string `json:"sshHostAlias"`
	EffectiveDisplayName string `json:"effectiveDisplayName"`
	PreferredSSHHost     string `json:"preferredSSHHost"`
	CanonicalSSHHost     string `json:"canonicalSSHHost"`
	SSHEntryUpdated      bool   `json:"sshEntryUpdated"`
}

type PresentationError struct {
	Field    string
	Code     string
	Message  string
	Conflict string
}

func (e PresentationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func newPresentationError(field, code, message string) *PresentationError {
	return &PresentationError{Field: field, Code: code, Message: message}
}

func newConflictError(field, conflict, message string) *PresentationError {
	return &PresentationError{
		Field:    field,
		Code:     PresentationCodeConflict,
		Message:  message,
		Conflict: conflict,
	}
}

func NormalizeDisplayName(s string) (string, error) {
	normalized := strings.TrimSpace(norm.NFC.String(s))
	for _, r := range normalized {
		if unicode.IsControl(r) {
			return "", newPresentationError(
				PresentationFieldDisplayName,
				PresentationCodeInvalid,
				"display name must not contain control characters",
			)
		}
	}
	if utf8.RuneCountInString(normalized) > maxDisplayNameLength {
		return "", newPresentationError(
			PresentationFieldDisplayName,
			PresentationCodeInvalid,
			fmt.Sprintf("display name must be at most %d characters", maxDisplayNameLength),
		)
	}

	return normalized, nil
}

func NormalizeSSHHostAlias(s string) (string, error) {
	alias := strings.ToLower(strings.TrimSpace(s))
	if alias == "" {
		return "", nil
	}
	if strings.HasSuffix(alias, config.SSHHostSuffix) {
		return "", newPresentationError(
			PresentationFieldSSHHostAlias,
			PresentationCodeInvalid,
			fmt.Sprintf("ssh host alias must not include the %s suffix", config.SSHHostSuffix),
		)
	}
	if !sshHostAliasPattern.MatchString(alias) {
		return "", newPresentationError(
			PresentationFieldSSHHostAlias,
			PresentationCodeInvalid,
			"ssh host alias must be 1-48 characters of lowercase letters, digits and hyphens, "+
				"starting and ending with a letter or digit",
		)
	}

	return alias, nil
}

func normalizeAliasForID(id, s string) (string, error) {
	alias, err := NormalizeSSHHostAlias(s)
	if err != nil {
		return "", err
	}
	if strings.EqualFold(alias, id) {
		return "", nil
	}

	return alias, nil
}

func foldLabel(s string) string {
	return cases.Fold().String(norm.NFC.String(s))
}

func ValidatePresentation(
	devsyConfig *config.Config,
	id string,
	displayName, sshHostAlias string,
) error {
	label, err := NormalizeDisplayName(displayName)
	if err != nil {
		return err
	}
	alias, err := normalizeAliasForID(id, sshHostAlias)
	if err != nil {
		return err
	}
	if label == "" && alias == "" {
		return nil
	}

	others, err := listOtherWorkspaces(devsyConfig.DefaultContext, id)
	if err != nil {
		return err
	}
	if err := checkLabelConflict(others, label); err != nil {
		return err
	}
	if alias == "" {
		return nil
	}
	if err := checkAliasWorkspaceConflict(others, alias); err != nil {
		return err
	}

	paths, err := resolveSSHPaths(
		devsyConfig.ContextOption(config.ContextOptionSSHConfigPath),
		devsyConfig.ContextOption(config.ContextOptionSSHConfigIncludePath),
	)
	if err != nil {
		return err
	}

	return checkAliasSSHConflict(paths, id+config.SSHHostSuffix, alias)
}

func EditPresentation(req PresentationRequest) (*PresentationResult, error) {
	if req.DevsyConfig == nil || req.ID == "" {
		return nil, newPresentationError(
			PresentationFieldWorkspace,
			PresentationCodeInvalid,
			"a workspace id is required",
		)
	}

	unlock, err := lockPresentation()
	if err != nil {
		return nil, err
	}
	defer unlock()

	current, err := loadEditableWorkspace(req)
	if err != nil {
		return nil, err
	}
	next := *current
	if err := applyPresentationRequest(req, current, &next); err != nil {
		return nil, err
	}

	aliasChanged := next.SSHHostAlias != current.SSHHostAlias
	labelChanged := next.DisplayName != current.DisplayName
	if err := checkPresentationConflicts(req, &next, labelChanged, aliasChanged); err != nil {
		return nil, err
	}

	entryUpdated := false
	if labelChanged || aliasChanged {
		entryUpdated, err = commitPresentation(current, &next, aliasChanged)
		if err != nil {
			return nil, err
		}
	}

	return newPresentationResult(&next, entryUpdated), nil
}

func lockPresentation() (func(), error) {
	dir, err := config.DefaultPathManager().ConfigDir()
	if err != nil {
		return nil, fmt.Errorf("get devsy home: %w", err)
	}

	lock := flock.New(filepath.Join(dir, presentationLockFile))
	if err := lock.Lock(); err != nil {
		return nil, fmt.Errorf("acquire workspace presentation lock: %w", err)
	}

	return func() { _ = lock.Unlock() }, nil
}

func loadEditableWorkspace(req PresentationRequest) (*provider.Workspace, error) {
	current, err := provider.LoadWorkspaceConfig(req.DevsyConfig.DefaultContext, req.ID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, newPresentationError(
				PresentationFieldWorkspace,
				PresentationCodeNotFound,
				fmt.Sprintf("workspace %q not found", req.ID),
			)
		}

		return nil, fmt.Errorf("load workspace %s: %w", req.ID, err)
	}
	if current.IsPlatform() {
		return nil, newPresentationError(
			PresentationFieldWorkspace,
			PresentationCodeManaged,
			fmt.Sprintf("workspace %q is managed by a platform and cannot be edited", req.ID),
		)
	}
	if req.ExpectedUID != "" && req.ExpectedUID != current.UID {
		return nil, newPresentationError(
			PresentationFieldWorkspace,
			PresentationCodeStale,
			fmt.Sprintf("workspace %q was recreated since it was loaded", req.ID),
		)
	}

	return current, nil
}

func applyPresentationRequest(
	req PresentationRequest,
	current, next *provider.Workspace,
) error {
	if req.ExpectedDisplayName != nil {
		expected, err := NormalizeDisplayName(*req.ExpectedDisplayName)
		if err != nil {
			return err
		}
		if expected != current.DisplayName && expected != current.EffectiveDisplayName() {
			return newPresentationError(
				PresentationFieldDisplayName,
				PresentationCodeStale,
				"display name changed since it was loaded",
			)
		}
	}
	if req.ExpectedSSHHostAlias != nil {
		expected, err := normalizeAliasForID(current.ID, *req.ExpectedSSHHostAlias)
		if err != nil {
			return err
		}
		if expected != current.SSHHostAlias {
			return newPresentationError(
				PresentationFieldSSHHostAlias,
				PresentationCodeStale,
				"ssh host alias changed since it was loaded",
			)
		}
	}
	if req.DisplayName != nil {
		label, err := NormalizeDisplayName(*req.DisplayName)
		if err != nil {
			return err
		}
		next.DisplayName = label
	}
	if req.SSHHostAlias != nil {
		alias, err := normalizeAliasForID(current.ID, *req.SSHHostAlias)
		if err != nil {
			return err
		}
		next.SSHHostAlias = alias
	}

	return nil
}

func checkPresentationConflicts(
	req PresentationRequest,
	next *provider.Workspace,
	labelChanged, aliasChanged bool,
) error {
	needsLabel := labelChanged && next.DisplayName != ""
	needsAlias := aliasChanged && next.SSHHostAlias != ""
	if !needsLabel && !needsAlias {
		return nil
	}

	others, err := listOtherWorkspaces(req.DevsyConfig.DefaultContext, next.ID)
	if err != nil {
		return err
	}
	if needsLabel {
		if err := checkLabelConflict(others, next.DisplayName); err != nil {
			return err
		}
	}
	if !needsAlias {
		return nil
	}
	if err := checkAliasWorkspaceConflict(others, next.SSHHostAlias); err != nil {
		return err
	}
	paths, err := resolveSSHPaths(next.SSHConfigPath, next.SSHConfigIncludePath)
	if err != nil {
		return err
	}

	return checkAliasSSHConflict(paths, next.CanonicalSSHHost(), next.SSHHostAlias)
}

func commitPresentation(
	current, next *provider.Workspace,
	aliasChanged bool,
) (bool, error) {
	if !aliasChanged {
		return false, provider.SaveWorkspacePresentation(next)
	}

	paths, err := resolveSSHPaths(current.SSHConfigPath, current.SSHConfigIncludePath)
	if err != nil {
		return false, err
	}
	restore, updated, err := ssh.ReplaceHostAliases(
		paths.config,
		paths.include,
		next.CanonicalSSHHost(),
		next.PreferredSSHHost(),
	)
	if err != nil {
		return false, fmt.Errorf("update ssh config: %w", err)
	}

	if err := provider.SaveWorkspacePresentation(next); err != nil {
		if updated && restore != nil {
			if rollbackErr := restore(); rollbackErr != nil {
				return false, errors.Join(
					err,
					fmt.Errorf("restore ssh config: %w", rollbackErr),
				)
			}
		}

		return false, err
	}

	return updated, nil
}

func newPresentationResult(ws *provider.Workspace, entryUpdated bool) *PresentationResult {
	return &PresentationResult{
		ID:                   ws.ID,
		UID:                  ws.UID,
		Context:              ws.Context,
		DisplayName:          ws.DisplayName,
		SSHHostAlias:         ws.SSHHostAlias,
		EffectiveDisplayName: ws.EffectiveDisplayName(),
		PreferredSSHHost:     ws.PreferredSSHHost(),
		CanonicalSSHHost:     ws.CanonicalSSHHost(),
		SSHEntryUpdated:      entryUpdated,
	}
}

func listOtherWorkspaces(contextName, id string) ([]*provider.Workspace, error) {
	all, err := ListLocalWorkspaces(contextName, false)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}

	others := make([]*provider.Workspace, 0, len(all))
	for _, ws := range all {
		if ws.ID != id {
			others = append(others, ws)
		}
	}

	return others, nil
}

func checkLabelConflict(others []*provider.Workspace, label string) error {
	if label == "" {
		return nil
	}

	folded := foldLabel(label)
	for _, other := range others {
		if foldLabel(other.EffectiveDisplayName()) == folded {
			return newConflictError(
				PresentationFieldDisplayName,
				other.ID,
				fmt.Sprintf("display name %q is already used by workspace %q", label, other.ID),
			)
		}
	}

	return nil
}

func checkAliasWorkspaceConflict(others []*provider.Workspace, alias string) error {
	host := alias + config.SSHHostSuffix
	for _, other := range others {
		if strings.EqualFold(other.CanonicalSSHHost(), host) ||
			strings.EqualFold(other.PreferredSSHHost(), host) {
			return newConflictError(
				PresentationFieldSSHHostAlias,
				other.ID,
				fmt.Sprintf("ssh host %q is already used by workspace %q", host, other.ID),
			)
		}
	}

	return nil
}

type sshPaths struct {
	config  string
	include string
}

func resolveSSHPaths(configPath, includePath string) (sshPaths, error) {
	resolved, err := ssh.ResolveSSHConfigPath(configPath)
	if err != nil {
		return sshPaths{}, err
	}

	paths := sshPaths{config: resolved}
	if includePath != "" {
		paths.include, err = ssh.ResolveSSHConfigPath(includePath)
		if err != nil {
			return sshPaths{}, err
		}
	}

	return paths, nil
}

func checkAliasSSHConflict(paths sshPaths, ownCanonicalHost, alias string) error {
	host := alias + config.SSHHostSuffix
	conflicts, err := ssh.FindHostConflicts(
		paths.config,
		paths.include,
		[]string{host},
		ownCanonicalHost,
	)
	if err != nil {
		return fmt.Errorf("check ssh config for host conflicts: %w", err)
	}
	if len(conflicts) == 0 {
		return nil
	}

	first := conflicts[0]

	return newConflictError(
		PresentationFieldSSHHostAlias,
		fmt.Sprintf("%s (%s)", first.Host, first.File),
		fmt.Sprintf("ssh host %q is already defined in %s", first.Host, first.File),
	)
}
