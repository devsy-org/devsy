package dependencies

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/blang/semver/v4"
)

const (
	metadataRegular    = "regular"
	metadataExecutable = "msb"
	metadataFirmware   = "libkrunfw.so.5.6.1"
)

// Receipt describes validated archive content, not current file identity, ownership,
// runtime readiness, or permission to execute or remove files.
type Receipt struct {
	SchemaVersion int                `json:"schemaVersion"`
	Identity      ReceiptIdentity    `json:"identity"`
	RecipeID      string             `json:"recipeId"`
	RecipeSHA256  string             `json:"recipeSha256"`
	SourceURL     string             `json:"sourceUrl"`
	InstalledAt   time.Time          `json:"installedAt"`
	Layout        string             `json:"layout"`
	Components    []ReceiptComponent `json:"components"`
	Validation    ReceiptValidation  `json:"validation"`
}

// ReceiptIdentity names an immutable bundle using the full archive digest.
type ReceiptIdentity struct {
	ToolID         string `json:"toolId"`
	RuntimeVersion string `json:"runtimeVersion"`
	Platform       string `json:"platform"`
	ArchiveSHA256  string `json:"archiveSha256"`
}

type ReceiptComponent struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// ReceiptValidation records only staging checks; it is not a live verification.
type ReceiptValidation struct {
	Archive string `json:"archive"`
	Layout  string `json:"layout"`
}

func (r Receipt) validate() error {
	if err := r.Identity.validate(); err != nil {
		return err
	}
	if !plainName(r.RecipeID) || !validSHA256(r.RecipeSHA256) || !metadataTime(r.InstalledAt) {
		return errors.New("invalid receipt recipe identity or installation time")
	}
	if err := r.validateSource(); err != nil {
		return err
	}
	return r.validateLayout()
}

func (r Receipt) validateSource() error {
	expectedSource := "https://github.com/superradcompany/microsandbox/releases/download/v" +
		strings.TrimPrefix(r.Identity.RuntimeVersion, "v") + "/microsandbox-linux-x86_64.tar.gz"
	if r.SourceURL != expectedSource {
		return errors.New(
			"receipt source must name the canonical credential-free upstream release asset",
		)
	}
	return nil
}

func (r Receipt) validateLayout() error {
	if r.Layout != "flat_verbatim_no_links" || len(r.Components) != 2 {
		return errors.New("unsupported receipt layout or component inventory")
	}
	if r.Validation != (ReceiptValidation{Archive: "sha256_verified", Layout: "validated"}) {
		return errors.New("receipt requires successful archive and layout validation")
	}
	return validateReceiptComponents(r.Components)
}

func (id ReceiptIdentity) validate() error {
	if id.ToolID != toolMicrosandbox || id.Platform != platformLinuxAMD64 ||
		!validSHA256(id.ArchiveSHA256) {
		return errors.New("unsupported receipt tool/platform or invalid archive digest")
	}
	if _, err := semver.Parse(strings.TrimPrefix(id.RuntimeVersion, "v")); err != nil {
		return errors.New("invalid receipt runtime semver")
	}
	return nil
}

func validateReceiptComponents(components []ReceiptComponent) error {
	seen := make(map[string]bool)
	for _, component := range components {
		if !slices.Contains([]string{metadataExecutable, metadataFirmware}, component.Path) ||
			seen[component.Path] {
			return errors.New("receipt requires the reviewed flat msb and versioned firmware pair")
		}
		seen[component.Path] = true
		if component.Type != metadataRegular || !validSHA256(component.SHA256) ||
			component.Size <= 0 {
			return errors.New("receipt component must be a hashed, nonempty regular file")
		}
	}
	return nil
}
