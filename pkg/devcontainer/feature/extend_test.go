package feature

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/suite"
)

const (
	testNodeFeatureID = "ghcr.io/devcontainers/features/node"
	testFeatureA      = "feature-a"
	testFeatureB      = "feature-b"
	testFeatureC      = "feature-c"
	testVersion23     = "2.3"
)

type ExtendTestSuite struct {
	suite.Suite
}

func TestExtendTestSuite(t *testing.T) {
	suite.Run(t, new(ExtendTestSuite))
}

func (suite *ExtendTestSuite) TestCreateFeatureLookup() {
	features := []*config.FeatureSet{
		{ConfigID: testFeatureA},
		{ConfigID: testFeatureB},
		{ConfigID: testFeatureC},
	}

	lookup := buildFeatureLookupMap(features)
	suite.Len(lookup, 3)

	for _, feature := range features {
		suite.Equal(feature, lookup[feature.ConfigID])
	}
}

type hardDependencyTestCase struct {
	name                string
	feature             *config.FeatureSet
	originalID          string
	normalizedID        string
	expectedIsDuplicate bool
}

func hardDependencyTestCases() []hardDependencyTestCase {
	return []hardDependencyTestCase{
		{
			name: "exact match in dependsOn",
			feature: &config.FeatureSet{
				Config: &config.FeatureConfig{
					DependsOn: config.DependsOnField{testFeatureNode: map[string]any{}},
				},
			},
			originalID:          testFeatureNode,
			normalizedID:        testFeatureNode,
			expectedIsDuplicate: true,
		},
		{
			name: "normalized match in dependsOn",
			feature: &config.FeatureSet{
				Config: &config.FeatureConfig{
					DependsOn: config.DependsOnField{testNodeFeatureID: map[string]any{}},
				},
			},
			originalID:          testNodeFeatureID + ":latest",
			normalizedID:        testNodeFeatureID,
			expectedIsDuplicate: true,
		},
		{
			name: "no match",
			feature: &config.FeatureSet{
				Config: &config.FeatureConfig{
					DependsOn: config.DependsOnField{"python": map[string]any{}},
				},
			},
			originalID:          testFeatureNode,
			normalizedID:        testFeatureNode,
			expectedIsDuplicate: false,
		},
		{
			name: "empty dependsOn",
			feature: &config.FeatureSet{
				Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}},
			},
			originalID:          testFeatureNode,
			normalizedID:        testFeatureNode,
			expectedIsDuplicate: false,
		},
	}
}

func (suite *ExtendTestSuite) TestHasHardDependency() {
	for _, testCase := range hardDependencyTestCases() {
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
			ConfigID: normalizeFeatureID(testFeatureA),
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{
					testFeatureB: map[string]any{},
				},
			},
		},
		{
			ConfigID: normalizeFeatureID(testFeatureB),
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{
					testFeatureA: map[string]any{},
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
					testNodeFeatureID: map[string]any{},
				},
				InstallsAfter: []string{testNodeFeatureID},
			},
		},
		{
			ConfigID: testNodeFeatureID,
			Config: &config.FeatureConfig{
				DependsOn:     config.DependsOnField{},
				InstallsAfter: []string{},
			},
		},
	}

	installationOrder, err := getOrderedFeatureSets(features)
	suite.Require().NoError(err)
	suite.Len(installationOrder, 2)
	suite.Equal(testNodeFeatureID, installationOrder[0].ConfigID)
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
			ConfigID: normalizeFeatureID(testFeatureA),
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{testFeatureB: map[string]any{}},
			},
		},
		{
			ConfigID: normalizeFeatureID(testFeatureB),
			Config:   &config.FeatureConfig{DependsOn: config.DependsOnField{}},
		},
	}

	order, err := getSortedFeatureSets(devContainer, features)
	suite.Require().NoError(err)

	suite.Len(order, 2)
	expB := normalizeFeatureID(testFeatureB)
	expA := normalizeFeatureID(testFeatureA)
	if order[0].ConfigID != expB || order[1].ConfigID != expA {
		suite.Failf(
			"Order mismatch",
			"Expected [%s, %s], got [%s, %s]",
			expB,
			expA,
			order[0].ConfigID,
			order[1].ConfigID,
		)
	}
}

func (suite *ExtendTestSuite) TestComputeFeatureOrder_OverrideViolatesDependsOn() {
	devContainer := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			OverrideFeatureInstallOrder: []string{testFeatureA, testFeatureB},
		},
	}

	features := []*config.FeatureSet{
		{
			ConfigID: testFeatureA,
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{testFeatureB: map[string]any{}},
			},
		},
		{ConfigID: testFeatureB, Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
	}

	_, err := getSortedFeatureSets(devContainer, features)
	suite.Error(err)
	suite.Contains(err.Error(), "overrideFeatureInstallOrder")
	suite.Contains(err.Error(), "dependency")
}

func (suite *ExtendTestSuite) TestComputeFeatureOrder_ValidOverride() {
	devContainer := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			OverrideFeatureInstallOrder: []string{testFeatureB, testFeatureA},
		},
	}

	features := []*config.FeatureSet{
		{
			ConfigID: testFeatureA,
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{testFeatureB: map[string]any{}},
			},
		},
		{ConfigID: testFeatureB, Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
	}

	order, err := getSortedFeatureSets(devContainer, features)
	suite.Require().NoError(err)
	suite.Len(order, 2)
	suite.Equal(testFeatureB, order[0].ConfigID)
	suite.Equal(testFeatureA, order[1].ConfigID)
}

func (suite *ExtendTestSuite) TestComputeFeatureOrder_PartialOverride() {
	devContainer := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			OverrideFeatureInstallOrder: []string{testFeatureC},
		},
	}

	features := []*config.FeatureSet{
		{
			ConfigID: testFeatureA,
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{testFeatureB: map[string]any{}},
			},
		},
		{ConfigID: testFeatureB, Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
		{ConfigID: testFeatureC, Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
	}

	order, err := getSortedFeatureSets(devContainer, features)
	suite.Require().NoError(err)
	suite.Len(order, 3)

	if order[0].ConfigID != testFeatureC {
		suite.Failf("First element mismatch", "Expected feature-c first, got %s", order[0].ConfigID)
	}
}

func (suite *ExtendTestSuite) TestBuildOverridePriority() {
	features := []*config.FeatureSet{
		{ConfigID: testFeatureA, Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
		{ConfigID: testFeatureB, Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
		{ConfigID: testFeatureC, Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
	}
	lookup := buildFeatureLookupMap(features)

	overrideOrder := []string{testFeatureC, testFeatureA}
	priority := buildOverridePriority(overrideOrder, lookup)

	suite.Equal(0, priority[testFeatureC])
	suite.Equal(1, priority[testFeatureA])
	_, hasB := priority[testFeatureB]
	suite.False(hasB)
}

func (suite *ExtendTestSuite) TestOverridePriorityAffectsSortOrder() {
	devContainer := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			OverrideFeatureInstallOrder: []string{testFeatureC, testFeatureA},
		},
	}

	features := []*config.FeatureSet{
		{ConfigID: testFeatureA, Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
		{ConfigID: testFeatureB, Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
		{ConfigID: testFeatureC, Config: &config.FeatureConfig{DependsOn: config.DependsOnField{}}},
	}

	order, err := getSortedFeatureSets(devContainer, features)
	suite.Require().NoError(err)
	suite.Len(order, 3)
	suite.Equal(testFeatureC, order[0].ConfigID)
	suite.Equal(testFeatureA, order[1].ConfigID)
	suite.Equal(testFeatureB, order[2].ConfigID)
}

func (suite *ExtendTestSuite) TestExtractFeatureByID() {
	features := []*config.FeatureSet{
		{ConfigID: testFeatureA},
		{ConfigID: testFeatureB},
	}

	found := extractFeatureByID(features, testFeatureB)
	if found == nil || found.ConfigID != testFeatureB {
		suite.Fail("Expected to find feature-b")
	}

	notFound := extractFeatureByID(features, testFeatureC)
	if notFound != nil {
		suite.Fail("Expected not to find feature-c")
	}
}

func (suite *ExtendTestSuite) TestContainsFeature() {
	features := []*config.FeatureSet{
		{ConfigID: testFeatureA},
		{ConfigID: testFeatureB},
	}

	if !containsFeature(features, testFeatureA) {
		suite.Fail("Expected to contain feature-a")
	}

	if containsFeature(features, testFeatureC) {
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
		testFeatureA: {
			ConfigID: testFeatureA,
			Config: &config.FeatureConfig{
				LegacyIds: []string{testFeatureB},
				DependsOn: config.DependsOnField{},
			},
		},
		testFeatureB: {
			ConfigID: testFeatureB,
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{},
			},
		},
		"consumer": {
			ConfigID: "consumer",
			Config: &config.FeatureConfig{
				DependsOn: config.DependsOnField{
					testFeatureB: map[string]any{},
				},
			},
		},
	}

	resolved, err := resolveDependencies(&featureProcessor{}, features)
	suite.Require().NoError(err)
	suite.Len(resolved, 3)
	suite.NotNil(resolved[testFeatureB])
}

func (suite *ExtendTestSuite) TestVersionAwareDeduplication_SameConfigSameVersion() {
	features := map[string]*config.FeatureSet{}

	f1 := &config.FeatureSet{
		ConfigID: testNodeFeatureID,
		Version:  "1",
		Config:   &config.FeatureConfig{DependsOn: config.DependsOnField{}},
	}
	f2 := &config.FeatureSet{
		ConfigID: testNodeFeatureID,
		Version:  "1",
		Config:   &config.FeatureConfig{DependsOn: config.DependsOnField{}},
	}

	key := featureDeduplicationKey(testNodeFeatureID, "1")
	suite.Equal(testNodeFeatureID+":1", key)

	features[featureDeduplicationKey(f1.ConfigID, f1.Version)] = f1
	features[featureDeduplicationKey(f2.ConfigID, f2.Version)] = f2
	suite.Len(features, 1)
}

func (suite *ExtendTestSuite) TestVersionAwareDeduplication_SameConfigDifferentVersion() {
	features := map[string]*config.FeatureSet{}

	v1 := &config.FeatureSet{
		ConfigID: testNodeFeatureID,
		Version:  "1",
		Config:   &config.FeatureConfig{DependsOn: config.DependsOnField{}},
	}
	v2 := &config.FeatureSet{
		ConfigID: testNodeFeatureID,
		Version:  "2",
		Config:   &config.FeatureConfig{DependsOn: config.DependsOnField{}},
	}

	features[featureDeduplicationKey(v1.ConfigID, v1.Version)] = v1
	features[featureDeduplicationKey(v2.ConfigID, v2.Version)] = v2
	suite.Len(features, 2)
}

func (suite *ExtendTestSuite) TestVersionAwareDeduplication_EmptyVersionIsDuplicate() {
	features := map[string]*config.FeatureSet{}

	f1 := &config.FeatureSet{
		ConfigID: testNodeFeatureID,
		Version:  "",
		Config:   &config.FeatureConfig{DependsOn: config.DependsOnField{}},
	}
	f2 := &config.FeatureSet{
		ConfigID: testNodeFeatureID,
		Version:  "",
		Config:   &config.FeatureConfig{DependsOn: config.DependsOnField{}},
	}

	features[featureDeduplicationKey(f1.ConfigID, f1.Version)] = f1
	features[featureDeduplicationKey(f2.ConfigID, f2.Version)] = f2
	suite.Len(features, 1)
}

func (suite *ExtendTestSuite) TestExtractVersionFromFeatureID() {
	tests := []struct {
		input    string
		expected string
	}{
		{testNodeFeatureID + ":1", "1"},
		{testNodeFeatureID + ":2", "2"},
		{testNodeFeatureID + ":latest", ""},
		{testNodeFeatureID, ""},
		{testNodeFeatureID + ":v1", "1"},
		{testNodeFeatureID + ":v2.3", testVersion23},
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
		{testVersion23, testVersion23},
		{"v2.3", testVersion23},
	}

	for _, tc := range tests {
		suite.Run(tc.input, func() {
			suite.Equal(tc.expected, normalizeVersion(tc.input))
		})
	}
}

func (suite *ExtendTestSuite) TestContainsFeature_VersionAware() {
	features := []*config.FeatureSet{
		{ConfigID: testNodeFeatureID, Version: "1"},
		{ConfigID: testNodeFeatureID, Version: "2"},
	}

	suite.True(containsFeature(features, testNodeFeatureID+":1"))
	suite.True(containsFeature(features, testNodeFeatureID+":2"))
	suite.False(containsFeature(features, testNodeFeatureID+":3"))
	suite.False(containsFeature(features, testNodeFeatureID+":latest"))
}

func (suite *ExtendTestSuite) TestExtractFeatureByID_VersionAware() {
	features := []*config.FeatureSet{
		{ConfigID: testNodeFeatureID, Version: "1"},
		{ConfigID: testNodeFeatureID, Version: "2"},
	}

	found := extractFeatureByID(features, testNodeFeatureID+":1")
	suite.NotNil(found)
	suite.Equal("1", found.Version)

	found = extractFeatureByID(features, testNodeFeatureID+":2")
	suite.NotNil(found)
	suite.Equal("2", found.Version)

	notFound := extractFeatureByID(features, testNodeFeatureID+":3")
	suite.Nil(notFound)
}
