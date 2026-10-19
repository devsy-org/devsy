package dependencies

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/blang/semver/v4"
)

//go:embed catalog/v1.json
var embeddedCatalog string

type catalogDocument struct {
	SchemaVersion int       `json:"schemaVersion"`
	ID            string    `json:"id"`
	ManagedStatus string    `json:"managedStatus"`
	Bindings      []Binding `json:"bindings"`
	Recipes       []Recipe  `json:"recipes"`
}

type Catalog struct {
	document catalogDocument
	hash     string
}

func LoadCatalog() (*Catalog, error) {
	return decodeCatalog([]byte(embeddedCatalog))
}

func decodeCatalog(data []byte) (*Catalog, error) {
	if err := checkJSONValue(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return nil, fmt.Errorf("invalid dependency catalog JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document catalogDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode dependency catalog: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("dependency catalog has trailing data")
	}
	if err := document.validate(); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode dependency catalog identity: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return &Catalog{document: document, hash: hex.EncodeToString(digest[:])}, nil
}

func checkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch token {
	case nil:
		return errors.New("null catalog metadata is forbidden")
	case json.Delim('{'):
		return checkJSONObject(decoder)
	case json.Delim('['):
		for decoder.More() {
			if err := checkJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return nil
	}
}

func checkJSONObject(decoder *json.Decoder) error {
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return errors.New("duplicate or invalid JSON object key")
		}
		seen[key] = true
		if err := checkJSONValue(decoder); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	return err
}

func (c *Catalog) ID() string { return c.document.ID }

func (c *Catalog) Hash() string { return c.hash }

func (c *Catalog) ManagedStatus() string { return c.document.ManagedStatus }

func (c *Catalog) Bindings() []Binding {
	bindings := slices.Clone(c.document.Bindings)
	for i := range bindings {
		bindings[i].Platforms = slices.Clone(bindings[i].Platforms)
		bindings[i].Requirements = cloneRequirements(bindings[i].Requirements)
	}
	return bindings
}

func (c *Catalog) Recipes() []Recipe {
	recipes := slices.Clone(c.document.Recipes)
	for i := range recipes {
		recipes[i].Members = slices.Clone(recipes[i].Members)
		recipes[i].Prerequisites = slices.Clone(recipes[i].Prerequisites)
		recipes[i].Actions = slices.Clone(recipes[i].Actions)
	}
	return recipes
}

func (c *Catalog) Match(contract ProviderContract) (ContractMatch, error) {
	if err := validateContractFacts(contract); err != nil {
		return ContractMatch{}, err
	}
	for _, binding := range c.document.Bindings {
		if binding.Source == contract.Source {
			return matchBinding(binding, contract), nil
		}
	}
	return ContractMatch{ManagedReason: "dependency_policy_blocked"}, nil
}

func matchBinding(binding Binding, contract ProviderContract) ContractMatch {
	match := ContractMatch{
		BindingID: binding.ID, GuidanceID: binding.GuidanceID,
		ManagedReason: "dependency_recipe_unqualified", AdapterStatus: "not_applicable",
		ContractVerified: binding.ExecutionContract == contract.ExecutionContract,
	}
	if binding.Source.Kind == SourceGitHub {
		match.AdapterStatus = "version_unqualified"
		if contract.AdapterVersion == "" {
			match.AdapterStatus = "version_unknown"
		}
	}
	if !match.ContractVerified {
		match.ManagedReason = "dependency_policy_blocked"
		return match
	}
	imageBackend := contract.ImageBackend
	// The existing external driver defaults an omitted image backend to Docker.
	if imageBackend == "" {
		imageBackend = toolDocker
	}
	for _, requirement := range binding.Requirements {
		if requirement.WhenImageBackend == "" ||
			requirement.WhenImageBackend == imageBackend {
			match.Requirements = append(match.Requirements, requirement)
		}
	}
	match.Requirements = cloneRequirements(match.Requirements)
	// No released adapter range or qualified recipe exists; caller-supplied facts cannot enable installation.
	return match
}

func cloneRequirements(requirements []Requirement) []Requirement {
	cloned := slices.Clone(requirements)
	for i := range cloned {
		cloned[i].RequiredFor = slices.Clone(cloned[i].RequiredFor)
	}
	return cloned
}

func validateContractFacts(contract ProviderContract) error {
	if err := validateSource(contract.Source); err != nil {
		return err
	}
	if contract.AdapterVersion != "" {
		if _, err := semver.Parse(strings.TrimPrefix(contract.AdapterVersion, "v")); err != nil {
			return fmt.Errorf("invalid provider adapter version: %w", err)
		}
	}
	if contract.ManifestSHA256 != "" && !validSHA256(contract.ManifestSHA256) {
		return errors.New("invalid provider manifest SHA-256")
	}
	if contract.RuntimeCheckSchema < 0 {
		return errors.New("invalid runtime-check schema version")
	}
	if !slices.Contains([]string{"", toolDocker, imageBackendNone}, contract.ImageBackend) {
		return errors.New("unknown image backend")
	}
	return nil
}

func validateSource(source ProviderSource) error {
	identities := 0
	for _, value := range []string{source.InternalID, source.Repository, source.Location} {
		if value != "" {
			identities++
		}
	}
	if identities != 1 || !validSourceIdentity(source) {
		return errors.New("ambiguous or malformed provider source")
	}
	return nil
}

func validSourceIdentity(source ProviderSource) bool {
	switch source.Kind {
	case SourceInternal:
		return plainName(source.InternalID)
	case SourceGitHub:
		return canonicalRepository(source.Repository)
	case SourceURL, SourceFile:
		return source.Location != ""
	default:
		return false
	}
}

func canonicalRepository(repository string) bool {
	parts := strings.Split(repository, "/")
	return len(parts) == 3 && parts[0] == "github.com" && plainName(parts[1]) && plainName(parts[2])
}

func plainName(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	return strings.IndexFunc(value, func(r rune) bool {
		return !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz0123456789-_.", r)
	}) == -1
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}
