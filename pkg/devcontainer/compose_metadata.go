package devcontainer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/metadata"
)

const (
	composeContractLabel    = "devsy.compose.contract"
	composeRecreateGuidance = "rerun workspace up with the original configuration and --recreate"
)

type composeContract struct {
	Version   int    `json:"version"`
	Workspace string `json:"workspace"`
	Metadata  string `json:"metadata"`
	Features  string `json:"features"`
	Overlay   bool   `json:"overlay,omitempty"`
}

func metadataDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "v1:" + hex.EncodeToString(digest[:])
}

// This fingerprints build inputs without fetching features during container reuse.
// The resolved, ordered lifecycle chain is recorded separately in container metadata.
func composeFeatureSignature(c *config.DevContainerConfig) (string, error) {
	state := struct {
		Features map[string]any
		Order    []string
	}{Features: c.Features, Order: c.OverrideFeatureInstallOrder}
	if len(state.Features) == 0 {
		state.Features = nil
	}
	if len(state.Order) == 0 {
		state.Order = nil
	}
	value, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("fingerprint compose feature inputs: %w", err)
	}
	return metadataDigest(string(value)), nil
}

func verifiedComposeContract(details *config.ContainerDetails) (*composeContract, error) {
	labels := details.Config.Labels
	value := labels[composeContractLabel]
	if value == "" {
		if labels[overlayStructureLabel] != "" {
			if err := metadata.ValidateImageMetadata(
				labels[metadata.ImageMetadataLabel],
			); err != nil {
				return nil, fmt.Errorf(
					"invalid compose container lifecycle metadata: %w; %s",
					err,
					composeRecreateGuidance,
				)
			}
		}
		return nil, nil
	}
	contract, err := parseComposeContract(value)
	if err != nil {
		return nil, err
	}
	metadataValue := labels[metadata.ImageMetadataLabel]
	if metadataValue == "" || metadataDigest(metadataValue) != contract.Metadata {
		return nil, fmt.Errorf(
			"compose container lifecycle metadata is incomplete or changed; preserve workspace data and %s",
			composeRecreateGuidance,
		)
	}
	if err := metadata.ValidateImageMetadata(metadataValue); err != nil {
		return nil, fmt.Errorf(
			"invalid compose container lifecycle metadata: %w; %s",
			err,
			composeRecreateGuidance,
		)
	}
	return contract, nil
}

func parseComposeContract(value string) (*composeContract, error) {
	var contract composeContract
	if err := json.Unmarshal(
		[]byte(value),
		&contract,
	); err != nil || contract.Version != 1 || contract.Metadata == "" ||
		contract.Features == "" || contract.Workspace == "" {
		return nil, fmt.Errorf(
			"compose container has an invalid or unsupported lifecycle contract; %s",
			composeRecreateGuidance,
		)
	}
	return &contract, nil
}

func verifyCreatedComposeContainer(
	details *config.ContainerDetails,
	expectedContract string,
) error {
	if details == nil || expectedContract == "" ||
		details.Config.Labels[composeContractLabel] != expectedContract {
		return fmt.Errorf(
			"created compose container lifecycle contract does not match the creation plan; rerun workspace up with --recreate",
		)
	}
	_, err := verifiedComposeContract(details)
	return err
}

func (r *runner) verifyComposeIdentity(
	details *config.ContainerDetails,
	project, service string,
) error {
	labels := details.Config.Labels
	if labels[composeContractLabel] == "" {
		return nil
	}
	if labels[pkgconfig.ComposeProjectLabel] != project ||
		labels[pkgconfig.ComposeServiceLabel] != service {
		return fmt.Errorf(
			"compose container identity does not match the requested project and service",
		)
	}
	contract, err := parseComposeContract(labels[composeContractLabel])
	if err != nil {
		return err
	}
	if contract.Workspace != r.id {
		return fmt.Errorf(
			"compose container belongs to a different workspace; run workspace up explicitly",
		)
	}
	idLabels := r.idLabels
	for _, label := range idLabels {
		key, value, _ := strings.Cut(label, "=")
		if labels[key] != value {
			return fmt.Errorf(
				"compose container belongs to a different workspace; run workspace up explicitly",
			)
		}
	}
	return nil
}

func (r *runner) checkComposeFeatureRecreation(
	details *config.ContainerDetails,
	parsed *config.SubstitutedConfig,
	options UpOptions,
) error {
	if options.NoBuild || options.Recreate || len(parsed.Config.DockerComposeFile) == 0 {
		return nil
	}
	previous, err := r.previousComposeFeatureSignature(details)
	if err != nil || previous == "" {
		return err
	}
	current, err := composeFeatureSignature(parsed.Config)
	if err != nil {
		return err
	}
	if previous != current {
		return fmt.Errorf(
			"compose feature configuration changed; rerun workspace up with --recreate to install the requested features",
		)
	}
	return nil
}

func (r *runner) previousComposeFeatureSignature(details *config.ContainerDetails) (string, error) {
	contract, err := verifiedComposeContract(details)
	if err != nil {
		return "", err
	}
	if contract != nil {
		return contract.Features, nil
	}
	// A legacy result can establish input drift, but never supplies executable hooks.
	last := r.workspaceConfig.LastDevContainerConfig
	if last == nil || last.Config == nil {
		return "", nil
	}
	return composeFeatureSignature(last.Config)
}
