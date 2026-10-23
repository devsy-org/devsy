package dependencies

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/blang/semver/v4"
)

const maxCatalogArchiveBytes int64 = 1 << 30

func validateRecipes(recipes []Recipe) error {
	identities := make(map[string]bool)
	for _, recipe := range recipes {
		keys := []string{
			"id:" + recipe.ID,
			"tuple:" + recipe.ToolID + ":" + recipe.RuntimeVersion + ":" + recipe.Platform,
			"url:" + recipe.URL,
			"sha256:" + recipe.SHA256,
		}
		for _, key := range keys {
			if identities[key] {
				return fmt.Errorf("duplicate recipe identity %q", key)
			}
			identities[key] = true
		}
		if err := validateRecipe(recipe); err != nil {
			return fmt.Errorf("recipe %q: %w", recipe.ID, err)
		}
	}
	return nil
}

func validateRecipe(recipe Recipe) error {
	if recipe.Enabled {
		return errors.New(
			"enabled recipes require unavailable qualification and released-adapter gates",
		)
	}
	if !plainName(recipe.ID) || recipe.ToolID != toolMicrosandbox {
		return errors.New("unrecognized recipe identity or tool")
	}
	if err := validateRecipeQualification(recipe); err != nil {
		return err
	}
	if _, err := semver.Parse(strings.TrimPrefix(recipe.RuntimeVersion, "v")); err != nil {
		return fmt.Errorf("invalid upstream runtime version: %w", err)
	}
	if err := validateRecipeArtifact(recipe); err != nil {
		return err
	}
	if err := validateRecipeMembers(recipe); err != nil {
		return err
	}
	return validateRecipeChecks(recipe)
}

func validateRecipeQualification(recipe Recipe) error {
	if !slices.Contains(
		[]string{"not_run", "source_recorded_unqualified", "blocked"},
		recipe.QualificationStatus,
	) ||
		recipe.QualificationRecord != "" {
		return errors.New("no qualification records have been approved")
	}
	return nil
}

func validateRecipeArtifact(recipe Recipe) error {
	if recipe.Platform != platformLinuxAMD64 {
		return errors.New("no reviewed archive layout for this platform")
	}
	if recipe.ArchiveFormat != "tar.gz" || recipe.Layout != "flat_verbatim_no_links" {
		return errors.New("unsupported archive format or layout")
	}
	if !validSHA256(recipe.SHA256) {
		return errors.New("invalid archive SHA-256")
	}
	if !boundedArchiveSize(recipe.MaxArchiveBytes) || !boundedArchiveSize(recipe.MaxExpandedBytes) {
		return errors.New("archive size bounds must be positive and at most 1 GiB")
	}
	return validateRecipeURL(recipe)
}

func boundedArchiveSize(size int64) bool {
	return size > 0 && size <= maxCatalogArchiveBytes
}

func validateRecipeURL(recipe Recipe) error {
	expectedURL := "https://github.com/superradcompany/microsandbox/releases/download/" + recipe.RuntimeVersion +
		"/microsandbox-linux-x86_64.tar.gz"
	if !strings.HasPrefix(recipe.RuntimeVersion, "v") || recipe.URL != expectedURL {
		return errors.New("release asset must name an exact upstream version and allowlisted asset")
	}
	return nil
}

func validateRecipeMembers(recipe Recipe) error {
	if recipe.Executable != "msb" || recipe.Firmware != "libkrunfw.so.5.6.1" ||
		len(recipe.Members) != 2 {
		return errors.New("unsupported runtime pair or archive inventory")
	}
	seen := make(map[string]bool)
	var expandedBytes int64
	for _, member := range recipe.Members {
		if seen[member.Name] {
			return errors.New("duplicate archive member")
		}
		seen[member.Name] = true
		if err := validateRecipeMember(member, recipe); err != nil {
			return err
		}
		expandedBytes += member.MaxBytes
	}
	if expandedBytes > recipe.MaxExpandedBytes {
		return errors.New("member size bounds exceed expanded archive bound")
	}
	return nil
}

func validateRecipeMember(member ArchiveMember, recipe Recipe) error {
	if member.Name != recipe.Executable && member.Name != recipe.Firmware {
		return errors.New("archive member must name the reviewed flat runtime pair")
	}
	if member.Type != "regular" {
		return errors.New("non-regular archive member; links are forbidden")
	}
	if member.MaxBytes <= 0 || member.MaxBytes > recipe.MaxExpandedBytes {
		return errors.New("invalid archive member size bound")
	}
	return nil
}

func validateRecipeChecks(recipe Recipe) error {
	if recipe.ProbeID != "microsandbox_version" ||
		!slices.Equal(recipe.Prerequisites, []string{"kvm"}) {
		return errors.New("unrecognized recipe probe or prerequisites")
	}
	if !slices.Equal(recipe.Actions, []string{"install", "select", "remove"}) {
		return errors.New("unrecognized recipe actions")
	}
	return nil
}
