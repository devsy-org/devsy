package feature

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/suite"
)

type ExtendTestSuite struct {
	suite.Suite
}

func TestExtendTestSuite(t *testing.T) {
	suite.Run(t, new(ExtendTestSuite))
}

func (suite *ExtendTestSuite) TestCreateFeatureLookup() {
	features := []*config.FeatureSet{
		{ConfigID: "feature-a"},
		{ConfigID: "feature-b"},
		{ConfigID: "feature-c"},
	}

	lookup := buildFeatureLookupMap(features)
	suite.Len(lookup, 3)

	for _, feature := range features {
		suite.Equal(feature, lookup[feature.ConfigID])
	}
}

func (suite *ExtendTestSuite) TestHasHardDependency() {
	tests := []struct {
		name                string
		feature             *config.FeatureSet
		originalID          string
		normalizedID        string
		expectedIsDuplicate bool
	}{
		{
			name: "exact match in dependsOn",
			feature: &config.FeatureSet{
				Config: &config.FeatureConfig{
					DependsOn: config.DependsOnField{
						"node": map[string]any{},
					},
				},
			},
			originalID:          "node",
			normalizedID:        "node",
			expectedIsDuplicate: true,
		},
		{
			name: "normalized match in dependsOn",
			feature: &config.FeatureSet{
				Config: &config.FeatureConfig{
					DependsOn: config.DependsOnField{
						"ghcr.io/devcontainers/features/node": map[string]any{},
					},
				},
			},
			originalID:          "ghcr.io/devcontainers/features/node:latest",
			normalizedID:        "ghcr.io/devcontainers/features/node",
			expectedIsDuplicate: true,
		},
		{
			name: "no match",
			feature: &config.FeatureSet{
				Config: &config.FeatureConfig{
					DependsOn: config.DependsOnField{
						"python": map[string]any{},
					},
				},
			},
			originalID:          "node",
			normalizedID:        "node",
			expectedIsDuplicate: false,
		},
		{
			name: "empty dependsOn",
			feature: &config.FeatureSet{
				Config: &config.FeatureConfig{
					DependsOn: config.DependsOnField{},
				},
			},
			originalID:          "node",
			normalizedID:        "node",
			expectedIsDuplicate: false,
		},
	}

	for _, testCase := range tests {
		suite.Run(testCase.name, func() {
			actualIsDuplicate := hasHardDependency(
				testCase.feature,
				testCase.originalID,
				testCase.normalizedID,
			)
			suite.Equal(testCase.expectedIsDuplicate, actualIsDuplicate)
		})
	}
}

func (suite *ExtendTestSuite) TestComputeAutomaticFeatureOrder_SimpleDependency() {
	features := []*config.FeatureSet{
		{
			ConfigID: normalizeFeatureID("dependent-feature"),
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{
					"dependency-feature": map[string]any{},
				},
			},
		},
		{
			ConfigID: normalizeFeatureID("dependency-feature"),
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{},
			},
		},
	}

	installationOrder, err := getOrderedFeatureSets(features)
	suite.Require().NoError(err)

	suite.Len(installationOrder, 2)
	expectedDependency := normalizeFeatureID("dependency-feature")
	expectedDependent := normalizeFeatureID("dependent-feature")

	suite.Equal(expectedDependency, installationOrder[0].ConfigID)
	suite.Equal(expectedDependent, installationOrder[1].ConfigID)
}

func (suite *ExtendTestSuite) TestComputeAutomaticFeatureOrder_DependsOnAndInstallsAfter() {
	features := []*config.FeatureSet{
		{
			ConfigID: normalizeFeatureID("feature-with-both-dependencies"),
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{
					"shared-dependency": map[string]any{},
				},
				InstallsAfter: []string{"shared-dependency"},
			},
		},
		{
			ConfigID: normalizeFeatureID("shared-dependency"),
			Config: &config.FeatureConfig{
				DependsOn:     config.DependsOnField{},
				InstallsAfter: []string{},
			},
		},
	}

	installationOrder, err := getOrderedFeatureSets(features)
	suite.Require().NoError(err)

	suite.Len(installationOrder, 2)
	expectedSharedDep := normalizeFeatureID("shared-dependency")
	expectedFeatureWithBoth := normalizeFeatureID("feature-with-both-dependencies")

	suite.Equal(expectedSharedDep, installationOrder[0].ConfigID)
	suite.Equal(expectedFeatureWithBoth, installationOrder[1].ConfigID)
}

func (suite *ExtendTestSuite) TestComputeAutomaticFeatureOrder_OnlyInstallsAfter() {
	features := []*config.FeatureSet{
		{
			ConfigID: normalizeFeatureID("feature-with-soft-dependency"),
			Config: &config.FeatureConfig{
				DependsOn:     config.DependsOnField{},
				InstallsAfter: []string{"preferred-first-feature"},
			},
		},
		{
			ConfigID: normalizeFeatureID("preferred-first-feature"),
			Config: &config.FeatureConfig{
				DependsOn:     config.DependsOnField{},
				InstallsAfter: []string{},
			},
		},
	}

	installationOrder, err := getOrderedFeatureSets(features)
	suite.Require().NoError(err)

	suite.Len(installationOrder, 2)
	expectedPreferredFirst := normalizeFeatureID("preferred-first-feature")
	expectedFeatureWithSoft := normalizeFeatureID("feature-with-soft-dependency")

	suite.Equal(expectedPreferredFirst, installationOrder[0].ConfigID)
	suite.Equal(expectedFeatureWithSoft, installationOrder[1].ConfigID)
}

func (suite *ExtendTestSuite) TestComputeAutomaticFeatureOrder_ChainedDependencies() {
	features := []*config.FeatureSet{
		{
			ConfigID: normalizeFeatureID("top-level-feature"),
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{
					"middle-level-feature": map[string]any{},
				},
			},
		},
		{
			ConfigID: normalizeFeatureID("middle-level-feature"),
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{
					"base-level-feature": map[string]any{},
				},
			},
		},
		{
			ConfigID: normalizeFeatureID("base-level-feature"),
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{},
			},
		},
	}

	installationOrder, err := getOrderedFeatureSets(features)
	suite.Require().NoError(err)

	suite.Len(installationOrder, 3)

	expectedOrder := []string{
		normalizeFeatureID("base-level-feature"),
		normalizeFeatureID("middle-level-feature"),
		normalizeFeatureID("top-level-feature"),
	}
	for i, expectedFeatureID := range expectedOrder {
		if installationOrder[i].ConfigID != expectedFeatureID {
			suite.Failf(
				"Position mismatch",
				"Position %d: expected %s, got %s",
				i,
				expectedFeatureID,
				installationOrder[i].ConfigID,
			)
		}
	}
}

func (suite *ExtendTestSuite) TestComputeAutomaticFeatureOrder_CircularDependency() {
	features := []*config.FeatureSet{
		{
			ConfigID: normalizeFeatureID("feature-a"),
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{
					"feature-b": map[string]any{},
				},
			},
		},
		{
			ConfigID: normalizeFeatureID("feature-b"),
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{
					"feature-a": map[string]any{},
				},
			},
		},
	}

	_, err := getOrderedFeatureSets(features)
	suite.Error(err)
	suite.Contains(err.Error(), "circular")
}

func (suite *ExtendTestSuite) TestFeatureOrderWithDependencies_SameDependsOnAndInstallsAfter() {
	features := []*config.FeatureSet{
		{
			ConfigID: "dev-code",
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{
					"ghcr.io/devcontainers/features/node": map[string]any{},
				},
				InstallsAfter: []string{"ghcr.io/devcontainers/features/node"},
			},
		},
		{
			ConfigID: "ghcr.io/devcontainers/features/node",
			Config: &config.FeatureConfig{
				DependsOn:     config.DependsOnField{},
				InstallsAfter: []string{},
			},
		},
	}

	installationOrder, err := getOrderedFeatureSets(features)
	suite.Require().NoError(err)
	suite.Len(installationOrder, 2)
	suite.Equal("ghcr.io/devcontainers/features/node", installationOrder[0].ConfigID)
	suite.Equal("dev-code", installationOrder[1].ConfigID)
}

func (suite *ExtendTestSuite) TestComputeFeatureOrder_NoOverride() {
	devContainer := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			OverrideFeatureInstallOrder: []string{},
		},
	}

	features := []*config.FeatureSet{
		{
			ConfigID: normalizeFeatureID("feature-a"),
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{"feature-b": map[string]any{}},
			},
		},
		{
			ConfigID: normalizeFeatureID("feature-b"),
			Config:   &config.FeatureConfig{DependsOn: config.DependsOnField{}},
		},
	}

	order, err := getSortedFeatureSets(devContainer, features)
	suite.Require().NoError(err)

	suite.Len(order, 2)
	expectedFeatureB := normalizeFeatureID("feature-b")
	expectedFeatureA := normalizeFeatureID("feature-a")
	if order[0].ConfigID != expectedFeatureB || order[1].ConfigID != expectedFeatureA {
		suite.Failf(
			"Order mismatch",
			"Expected [%s, %s], got [%s, %s]",
			expectedFeatureB,
			expectedFeatureA,
			order[0].ConfigID,
			order[1].ConfigID,
		)
	}
}

func (suite *ExtendTestSuite) TestComputeFeatureOrder_OverrideViolatesDependsOn() {
	devContainer := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			OverrideFeatureInstallOrder: []string{"feature-a", "feature-b"},
		},
	}

	features := []*config.FeatureSet{
		{
			ConfigID: "feature-a",
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{"feature-b": map[string]any{}},
			},
		},
		{ConfigID: "feature-b", Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
	}

	_, err := getSortedFeatureSets(devContainer, features)
	suite.Error(err)
	suite.Contains(err.Error(), "overrideFeatureInstallOrder")
	suite.Contains(err.Error(), "dependency")
}

func (suite *ExtendTestSuite) TestComputeFeatureOrder_ValidOverride() {
	devContainer := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			OverrideFeatureInstallOrder: []string{"feature-b", "feature-a"},
		},
	}

	features := []*config.FeatureSet{
		{
			ConfigID: "feature-a",
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{"feature-b": map[string]any{}},
			},
		},
		{ConfigID: "feature-b", Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
	}

	order, err := getSortedFeatureSets(devContainer, features)
	suite.Require().NoError(err)
	suite.Len(order, 2)
	suite.Equal("feature-b", order[0].ConfigID)
	suite.Equal("feature-a", order[1].ConfigID)
}

func (suite *ExtendTestSuite) TestComputeFeatureOrder_PartialOverride() {
	devContainer := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			OverrideFeatureInstallOrder: []string{"feature-c"},
		},
	}

	features := []*config.FeatureSet{
		{
			ConfigID: "feature-a",
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{"feature-b": map[string]any{}},
			},
		},
		{ConfigID: "feature-b", Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
		{ConfigID: "feature-c", Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
	}

	order, err := getSortedFeatureSets(devContainer, features)
	suite.Require().NoError(err)
	suite.Len(order, 3)

	if order[0].ConfigID != "feature-c" {
		suite.Failf("First element mismatch", "Expected feature-c first, got %s", order[0].ConfigID)
	}
}

func (suite *ExtendTestSuite) TestBuildOverridePriority() {
	features := []*config.FeatureSet{
		{ConfigID: "feature-a", Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
		{ConfigID: "feature-b", Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
		{ConfigID: "feature-c", Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
	}
	lookup := buildFeatureLookupMap(features)

	overrideOrder := []string{"feature-c", "feature-a"}
	priority := buildOverridePriority(overrideOrder, lookup)

	suite.Equal(0, priority["feature-c"])
	suite.Equal(1, priority["feature-a"])
	_, hasB := priority["feature-b"]
	suite.False(hasB)
}

func (suite *ExtendTestSuite) TestOverridePriorityAffectsSortOrder() {
	devContainer := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			OverrideFeatureInstallOrder: []string{"feature-c", "feature-a"},
		},
	}

	features := []*config.FeatureSet{
		{ConfigID: "feature-a", Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
		{ConfigID: "feature-b", Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
		{ConfigID: "feature-c", Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
	}

	order, err := getSortedFeatureSets(devContainer, features)
	suite.Require().NoError(err)
	suite.Len(order, 3)
	suite.Equal("feature-c", order[0].ConfigID)
	suite.Equal("feature-a", order[1].ConfigID)
	suite.Equal("feature-b", order[2].ConfigID)
}

func (suite *ExtendTestSuite) TestExtractFeatureByID() {
	features := []*config.FeatureSet{
		{ConfigID: "feature-a"},
		{ConfigID: "feature-b"},
	}

	found := extractFeatureByID(features, "feature-b")
	if found == nil || found.ConfigID != "feature-b" {
		suite.Fail("Expected to find feature-b")
	}

	notFound := extractFeatureByID(features, "feature-c")
	if notFound != nil {
		suite.Fail("Expected not to find feature-c")
	}
}

func (suite *ExtendTestSuite) TestContainsFeature() {
	features := []*config.FeatureSet{
		{ConfigID: "feature-a"},
		{ConfigID: "feature-b"},
	}

	if !containsFeature(features, "feature-a") {
		suite.Fail("Expected to contain feature-a")
	}

	if containsFeature(features, "feature-c") {
		suite.Fail("Expected not to contain feature-c")
	}
}

func (suite *ExtendTestSuite) TestFindContainerUsersUsesMetadataAndImageUserFallbacks() {
	effectiveMetadata := &config.ImageMetadataConfig{
		Config: []*config.ImageMetadata{{
			DevContainerConfigBase: config.DevContainerConfigBase{
				RemoteUser: "vscode",
			},
		}},
	}

	containerUser, remoteUser := findContainerUsers(effectiveMetadata, "", "nonroot")
	suite.Equal("nonroot", containerUser)
	suite.Equal("vscode", remoteUser)
}

func (suite *ExtendTestSuite) TestBuildLegacyIDMap() {
	features := map[string]*config.FeatureSet{
		"ghcr.io/org/features/current-name": {
			ConfigID: "ghcr.io/org/features/current-name",
			Config: &config.FeatureConfig{
				LegacyIds: []string{
					"ghcr.io/org/features/old-name",
					"ghcr.io/org/features/ancient-name",
				},
			},
		},
		"ghcr.io/org/features/other": {
			ConfigID: "ghcr.io/org/features/other",
			Config: &config.FeatureConfig{
				LegacyIds: []string{},
			},
		},
		"feature-no-config": {
			ConfigID: "feature-no-config",
			Config:   nil,
		},
	}

	legacyMap := buildLegacyIDMap(features)

	suite.Equal("ghcr.io/org/features/current-name", legacyMap["ghcr.io/org/features/old-name"])
	suite.Equal("ghcr.io/org/features/current-name", legacyMap["ghcr.io/org/features/ancient-name"])
	_, hasOther := legacyMap["ghcr.io/org/features/other"]
	suite.False(hasOther)
}

func (suite *ExtendTestSuite) TestBuildLegacyIDMap_NormalizesVersionTags() {
	features := map[string]*config.FeatureSet{
		"ghcr.io/org/features/node": {
			ConfigID: "ghcr.io/org/features/node",
			Config: &config.FeatureConfig{
				LegacyIds: []string{"ghcr.io/org/features/nodejs:1"},
			},
		},
	}

	legacyMap := buildLegacyIDMap(features)

	suite.Equal("ghcr.io/org/features/node", legacyMap["ghcr.io/org/features/nodejs"])
}

func (suite *ExtendTestSuite) TestResolveDependencies_LegacyIDResolution() {
	features := map[string]*config.FeatureSet{
		"current-feature": {
			ConfigID: "current-feature",
			Config: &config.FeatureConfig{
				LegacyIds: []string{"old-feature-name"},
				DependsOn: config.DependsOnField{},
			},
		},
		"consumer-feature": {
			ConfigID: "consumer-feature",
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{
					"old-feature-name": map[string]any{},
				},
			},
		},
	}

	resolved, err := resolveDependencies(&featureProcessor{}, features)
	suite.Require().NoError(err)
	suite.Len(resolved, 2)
	suite.NotNil(resolved["current-feature"])
	suite.NotNil(resolved["consumer-feature"])
}

func (suite *ExtendTestSuite) TestResolveDependencies_LegacyIDNotUsedWhenPrimaryExists() {
	features := map[string]*config.FeatureSet{
		"feature-a": {
			ConfigID: "feature-a",
			Config: &config.FeatureConfig{
				LegacyIds: []string{"feature-b"},
				DependsOn: config.DependsOnField{},
			},
		},
		"feature-b": {
			ConfigID: "feature-b",
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{},
			},
		},
		"consumer": {
			ConfigID: "consumer",
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{
					"feature-b": map[string]any{},
				},
			},
		},
	}

	resolved, err := resolveDependencies(&featureProcessor{}, features)
	suite.Require().NoError(err)
	suite.Len(resolved, 3)
	suite.NotNil(resolved["feature-b"])
}

func (suite *ExtendTestSuite) TestVersionAwareDeduplication_SameConfigSameVersion() {
	featureID := "ghcr.io/devcontainers/features/node" //nolint:goconst
	features := map[string]*config.FeatureSet{}

	f1 := &config.FeatureSet{
		ConfigID: featureID,
		Version:  "1",
		Config:   &config.FeatureConfig{DependsOn: config.DependsOnField{}},
	}
	f2 := &config.FeatureSet{
		ConfigID: featureID,
		Version:  "1",
		Config:   &config.FeatureConfig{DependsOn: config.DependsOnField{}},
	}

	key := featureDeduplicationKey(featureID, "1")
	suite.Equal(featureID+":1", key)

	features[featureDeduplicationKey(f1.ConfigID, f1.Version)] = f1
	features[featureDeduplicationKey(f2.ConfigID, f2.Version)] = f2
	suite.Len(features, 1)
}

func (suite *ExtendTestSuite) TestVersionAwareDeduplication_SameConfigDifferentVersion() {
	featureID := "ghcr.io/devcontainers/features/node"
	features := map[string]*config.FeatureSet{}

	v1 := &config.FeatureSet{
		ConfigID: featureID,
		Version:  "1",
		Config:   &config.FeatureConfig{DependsOn: config.DependsOnField{}},
	}
	v2 := &config.FeatureSet{
		ConfigID: featureID,
		Version:  "2",
		Config:   &config.FeatureConfig{DependsOn: config.DependsOnField{}},
	}

	features[featureDeduplicationKey(v1.ConfigID, v1.Version)] = v1
	features[featureDeduplicationKey(v2.ConfigID, v2.Version)] = v2
	suite.Len(features, 2)
}

func (suite *ExtendTestSuite) TestVersionAwareDeduplication_EmptyVersionIsDuplicate() {
	featureID := "ghcr.io/devcontainers/features/node"
	features := map[string]*config.FeatureSet{}

	f1 := &config.FeatureSet{
		ConfigID: featureID,
		Version:  "",
		Config:   &config.FeatureConfig{DependsOn: config.DependsOnField{}},
	}
	f2 := &config.FeatureSet{
		ConfigID: featureID,
		Version:  "",
		Config:   &config.FeatureConfig{DependsOn: config.DependsOnField{}},
	}

	features[featureDeduplicationKey(f1.ConfigID, f1.Version)] = f1
	features[featureDeduplicationKey(f2.ConfigID, f2.Version)] = f2
	suite.Len(features, 1)
}

func (suite *ExtendTestSuite) TestExtractVersionFromFeatureID() {
	nodeFeature := "ghcr.io/devcontainers/features/node" //nolint:goconst
	tests := []struct {
		input    string
		expected string
	}{
		{nodeFeature + ":1", "1"},
		{nodeFeature + ":2", "2"},
		{nodeFeature + ":latest", ""},
		{nodeFeature, ""},
		{nodeFeature + ":v1", "1"},
		{nodeFeature + ":v2.3", "2.3"}, //nolint:goconst
	}

	for _, tc := range tests {
		suite.Run(tc.input, func() {
			suite.Equal(tc.expected, extractVersionFromFeatureID(tc.input))
		})
	}
}

func (suite *ExtendTestSuite) TestNormalizeVersion() {
	tests := []struct {
		input    string
		expected string
	}{
		{"latest", ""},
		{"", ""},
		{"1", "1"},
		{"v1", "1"},
		{"2.3", "2.3"},
		{"v2.3", "2.3"},
	}

	for _, tc := range tests {
		suite.Run(tc.input, func() {
			suite.Equal(tc.expected, normalizeVersion(tc.input))
		})
	}
}

func (suite *ExtendTestSuite) TestContainsFeature_VersionAware() {
	featureID := "ghcr.io/devcontainers/features/node" //nolint:goconst
	features := []*config.FeatureSet{
		{ConfigID: featureID, Version: "1"},
		{ConfigID: featureID, Version: "2"},
	}

	suite.True(containsFeature(features, "ghcr.io/devcontainers/features/node:1"))
	suite.True(containsFeature(features, "ghcr.io/devcontainers/features/node:2"))
	suite.False(containsFeature(features, "ghcr.io/devcontainers/features/node:3"))
	suite.False(containsFeature(features, "ghcr.io/devcontainers/features/node:latest"))
}

func (suite *ExtendTestSuite) TestExtractFeatureByID_VersionAware() {
	featureID := "ghcr.io/devcontainers/features/node" //nolint:goconst
	features := []*config.FeatureSet{
		{ConfigID: featureID, Version: "1"},
		{ConfigID: featureID, Version: "2"},
	}

	found := extractFeatureByID(features, "ghcr.io/devcontainers/features/node:1")
	suite.NotNil(found)
	suite.Equal("1", found.Version)

	found = extractFeatureByID(features, "ghcr.io/devcontainers/features/node:2")
	suite.NotNil(found)
	suite.Equal("2", found.Version)

	notFound := extractFeatureByID(features, "ghcr.io/devcontainers/features/node:3")
	suite.Nil(notFound)
}
