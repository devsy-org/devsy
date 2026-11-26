package ssh

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
)

const (
	OwnerLinePrefix = "# Devsy Owner "

	ownerHashDomain = "devsy-ssh-block-owner-v1"
	ownerHashLength = 32
)

type Owner struct {
	Home    string
	Context string
	UID     string
}

type BlockOwnershipError struct {
	Host   string
	File   string
	Reason string
}

func (e *BlockOwnershipError) Error() string {
	return fmt.Sprintf(
		"ssh config block %q in %s %s; repair or remove it manually",
		e.Host, e.File, e.Reason,
	)
}

func NewOwner(contextName, uid string) (Owner, error) {
	home, err := config.DefaultPathManager().ConfigDir()
	if err != nil {
		return Owner{}, fmt.Errorf("resolve devsy home: %w", err)
	}

	return Owner{Home: home, Context: contextName, UID: uid}, nil
}

func (o Owner) Hash() string {
	sum := sha256.New()
	for _, part := range []string{ownerHashDomain, cleanOwnerHome(o.Home), o.Context, o.UID} {
		_, _ = fmt.Fprintf(sum, "%d:%s;", len(part), part)
	}

	return hex.EncodeToString(sum.Sum(nil))[:ownerHashLength]
}

func (o Owner) Line() string {
	return OwnerLinePrefix + o.Hash()
}

func cleanOwnerHome(home string) string {
	if home == "" {
		return ""
	}

	return filepath.Clean(home)
}

func resolveOwner(owner Owner, fallbackContext string) (Owner, error) {
	if owner.Context == "" {
		owner.Context = fallbackContext
	}
	if owner.Home == "" {
		defaults, err := NewOwner(owner.Context, owner.UID)
		if err != nil {
			return Owner{}, err
		}
		owner.Home = defaults.Home
	}
	owner.Home = cleanOwnerHome(owner.Home)

	return owner, nil
}

type blockPolicy struct {
	owner       Owner
	hash        string
	workspaceID string
	impliedHome string
}

func newBlockPolicy(owner Owner, workspaceID, explicitHome string) *blockPolicy {
	policy := &blockPolicy{
		owner:       owner,
		hash:        owner.Hash(),
		workspaceID: workspaceID,
	}
	if explicitHome == "" && os.Getenv(config.EnvHome) == "" {
		policy.impliedHome = cleanOwnerHome(owner.Home)
	}

	return policy
}

func newResolvedBlockPolicy(owner Owner, workspaceID, explicitHome string) (*blockPolicy, error) {
	resolved, err := resolveOwner(owner, "")
	if err != nil {
		return nil, err
	}

	return newBlockPolicy(resolved, workspaceID, explicitHome), nil
}

func (p *blockPolicy) claim(file, host string, block []string) error {
	if p == nil {
		return nil
	}

	recorded, hasOwner := blockOwnerHash(block)
	if hasOwner {
		if recorded == p.hash {
			return nil
		}

		return &BlockOwnershipError{
			Host: host,
			File: file,
			Reason: fmt.Sprintf(
				"belongs to a different devsy owner (%s), not home %q context %q workspace uid %q",
				recorded, p.owner.Home, p.owner.Context, p.owner.UID,
			),
		}
	}

	if p.legacyEvidence(block) {
		return nil
	}

	return &BlockOwnershipError{
		Host: host,
		File: file,
		Reason: fmt.Sprintf(
			"has no ownership record and no proxy command evidence "+
				"for home %q context %q workspace %q",
			p.owner.Home, p.owner.Context, p.workspaceID,
		),
	}
}

func (p *blockPolicy) owns(file, host string, block []string) bool {
	return p.claim(file, host, block) == nil
}

func blockOwnerHash(block []string) (string, bool) {
	if len(block) < 2 {
		return "", false
	}

	for _, line := range block[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		rest, ok := strings.CutPrefix(trimmed, OwnerLinePrefix)

		return strings.TrimSpace(rest), ok
	}

	return "", false
}

func (p *blockPolicy) legacyEvidence(block []string) bool {
	for _, line := range block {
		if strings.EqualFold(sshConfigKeyword(line), "proxycommand") {
			return p.proxyEvidence(configArguments(line))
		}
	}

	return tunnelEvidence(block)
}

func tunnelEvidence(block []string) bool {
	hostname, hasPort := "", false
	for _, line := range block[min(1, len(block)):] {
		args := configArguments(line)
		if len(args) != 1 {
			continue
		}
		switch strings.ToLower(sshConfigKeyword(line)) {
		case "hostname":
			if hostname == "" {
				hostname = args[0]
			}
		case "port":
			hasPort = true
		}
	}

	return hasPort && isLoopbackHostname(hostname)
}

func isLoopbackHostname(host string) bool {
	switch strings.ToLower(host) {
	case "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}

func (p *blockPolicy) proxyEvidence(args []string) bool {
	args, ok := workspaceSSHArgs(args)
	if !ok {
		return false
	}

	contextName, ok := flagValue(args, names.Flag(names.Context))
	if !ok || contextName != p.owner.Context {
		return false
	}

	return p.homeEvidence(args) && p.workspaceEvidence(args)
}

func workspaceSSHArgs(args []string) ([]string, bool) {
	idx := slices.Index(args, "workspace")
	if idx < 0 || idx+1 >= len(args) || args[idx+1] != "ssh" {
		return nil, false
	}
	rest := args[idx+2:]

	return rest, slices.Contains(rest, names.Flag(names.Stdio))
}

func (p *blockPolicy) workspaceEvidence(args []string) bool {
	userIdx := slices.Index(args, names.Flag(names.User))
	if userIdx < 0 || userIdx+2 >= len(args) {
		return false
	}

	return args[userIdx+2] == p.workspaceID
}

func (p *blockPolicy) homeEvidence(args []string) bool {
	home, ok := flagValue(args, names.Flag(names.Home))
	if !ok {
		return p.impliedHome != ""
	}

	return cleanOwnerHome(home) == cleanOwnerHome(p.owner.Home)
}

func flagValue(args []string, flag string) (string, bool) {
	idx := slices.Index(args, flag)
	if idx < 0 || idx+1 >= len(args) {
		return "", false
	}

	return args[idx+1], true
}

func workspaceIDFromHost(host string) string {
	return strings.TrimSuffix(host, config.SSHHostSuffix)
}
