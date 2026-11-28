package docker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/devsy-org/devsy/pkg/pool/provider"
)

// lockBridge must span ensureBridge and sidecar creation so release cannot remove the bridge in between.
func (p *Provider) lockBridge(ctx context.Context, pool string) (func(), error) {
	return p.bridgeLocks.lock(ctx, pool)
}

func (p *Provider) ensureBridge(ctx context.Context, pool string) (subnet, error) {
	n, err := p.findBridge(ctx, pool)
	if err != nil {
		return subnet{}, err
	}
	if n.ID == "" {
		n, err = p.createBridge(ctx, pool)
		if err != nil {
			return subnet{}, err
		}
	}
	return n.subnet()
}

func (p *Provider) createBridge(ctx context.Context, pool string) (netInfo, error) {
	name := bridgeName(pool)
	_, createErr := p.docker.Run(ctx, kindNetwork, verbCreate, "--opt", optICC+"="+iccDisabled,
		"--label", provider.LabelShared+"="+pool, "--", name)
	if createErr != nil && !bridgeCreateRace(createErr, name) {
		return netInfo{}, mapCreateError(createErr)
	}
	n, err := p.findBridge(ctx, pool)
	if err != nil || n.ID != "" {
		return n, err
	}
	if createErr != nil {
		return netInfo{}, createErr
	}
	return netInfo{}, fmt.Errorf("outbound bridge %q missing after create", name)
}

func bridgeCreateRace(err error, name string) bool {
	return dockerReports(err, name) &&
		(dockerReports(err, "already exists") || dockerReports(err, "already in use"))
}

// ReleaseShared implements provider.Provider.
func (p *Provider) ReleaseShared(ctx context.Context, pool string) error {
	if err := checkPool(ctx, pool); err != nil {
		return err
	}
	unlock, err := p.lockBridge(ctx, pool)
	if err != nil {
		return err
	}
	defer unlock()
	found, err := p.find(ctx, kinds[:1], scope{pool: pool})
	if err != nil || len(found) > 0 {
		return err
	}
	return p.releaseBridge(ctx, pool)
}

func (p *Provider) releaseBridge(ctx context.Context, pool string) error {
	n, err := p.findBridge(ctx, pool)
	if err != nil || n.ID == "" {
		return err
	}
	_, err = p.docker.Run(ctx, kindNetwork, "rm", "--", n.ID)
	if dockerReports(err, "has active endpoints") {
		return nil
	}
	return err
}

func (p *Provider) findBridge(ctx context.Context, pool string) (netInfo, error) {
	name := bridgeName(pool)
	ids, err := p.bridgeIDs(ctx, pool)
	if err != nil || len(ids) == 0 {
		return netInfo{}, err
	}
	if len(ids) != 1 {
		return netInfo{}, fmt.Errorf("refusing outbound bridge %q: multiple candidates", name)
	}
	raw, err := p.docker.Run(ctx, kindNetwork, "inspect", "--format", "{{json .}}", "--", ids[0])
	if err != nil {
		return netInfo{}, err
	}
	n, err := parseNetwork(raw)
	if err != nil {
		return netInfo{}, err
	}
	if err := n.verifyBridge(pool, ids[0]); err != nil {
		return netInfo{}, fmt.Errorf("refusing outbound bridge %q: %w", name, err)
	}
	return n, nil
}

func (p *Provider) bridgeIDs(ctx context.Context, pool string) ([]string, error) {
	k, _ := kindFor(kindNetwork)
	named, err := p.filteredIDs(ctx, k, []string{"--filter", "name=^" + bridgeName(pool) + "$"})
	if err != nil {
		return nil, err
	}
	labelled, err := p.filteredIDs(
		ctx,
		k,
		[]string{"--filter", "label=" + provider.LabelShared + "=" + pool},
	)
	if err != nil {
		return nil, err
	}
	named = append(named, labelled...)
	slices.Sort(named)
	return slices.Compact(named), nil
}

func (n netInfo) verifyBridge(pool, id string) error {
	if n.ID != id {
		return errors.New("network identity does not match")
	}
	return n.checkBridge(pool)
}

func dockerReports(err error, message string) bool {
	var ce *CommandError
	return errors.As(err, &ce) && strings.Contains(strings.ToLower(ce.Stderr), message)
}
