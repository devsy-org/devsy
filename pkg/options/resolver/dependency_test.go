package resolver

import (
	"context"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/graph"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/suite"
)

const (
	testParent         = "parent"
	testChild          = "child"
	testDefaultRef     = "${V}"
	testGlobalChildErr = "cannot use a global option as a dependency of a non-global option. " +
		`Option "child" used in children of option "parent"`
	testLocalChildErr = "cannot use a non-local option as a dependency of a local option. " +
		`Option "child" used in children of option "parent"`
)

type DependencyGuardTestSuite struct {
	suite.Suite
}

func TestDependencyGuardTestSuite(t *testing.T) {
	suite.Run(t, new(DependencyGuardTestSuite))
}

type dependencyCase struct {
	defs    config.OptionDefinitions
	values  map[string]config.OptionValue
	wantErr string
	from    string
	to      string
}

func childCase(parent, child *types.Option, wantErr string) dependencyCase {
	return dependencyCase{
		defs:    config.OptionDefinitions{testParent: parent, testChild: child},
		values:  map[string]config.OptionValue{testParent: {Children: []string{testChild}}},
		wantErr: wantErr,
		from:    testParent,
		to:      testChild,
	}
}

func variableCase(option, dep *types.Option, wantErr string) dependencyCase {
	return dependencyCase{
		defs:    config.OptionDefinitions{"opt": option, "V": dep},
		values:  map[string]config.OptionValue{},
		wantErr: wantErr,
		from:    "V",
		to:      "opt",
	}
}

func (suite *DependencyGuardTestSuite) TestChildScopeGuards() {
	cases := map[string]dependencyCase{
		"global child under non-global parent rejected": childCase(
			&types.Option{},
			&types.Option{Global: true},
			testGlobalChildErr,
		),
		"global child under global parent accepted": childCase(
			&types.Option{Global: true}, &types.Option{Global: true}, "",
		),
		"non-global child under global parent accepted": childCase(
			&types.Option{Global: true}, &types.Option{}, "",
		),
		"non-local child under local parent rejected": childCase(
			&types.Option{Local: true},
			&types.Option{},
			testLocalChildErr,
		),
		"local child under local parent accepted": childCase(
			&types.Option{Local: true}, &types.Option{Local: true}, "",
		),
		"local child under non-local parent accepted": childCase(
			&types.Option{}, &types.Option{Local: true}, "",
		),
	}

	for name, tc := range cases {
		suite.Run(name, func() { suite.runCase(tc) })
	}
}

func (suite *DependencyGuardTestSuite) TestVariableScopeGuards() {
	cases := map[string]dependencyCase{
		"global default referencing non-global variable rejected": variableCase(
			&types.Option{Global: true, Default: testDefaultRef},
			&types.Option{},
			`cannot use a non-global option as a dependency of a global option. Option "V" used in default of option "opt"`,
		),
		"global default referencing global variable accepted": variableCase(
			&types.Option{Global: true, Default: testDefaultRef}, &types.Option{Global: true}, "",
		),
		"non-local command referencing local variable rejected": variableCase(
			&types.Option{Command: "echo ${V}"},
			&types.Option{Local: true},
			`cannot use a local option as a dependency of a non-local option. Option "V" used in command of option "opt"`,
		),
		"local default referencing non-local variable accepted": variableCase(
			&types.Option{Local: true, Default: testDefaultRef}, &types.Option{}, "",
		),
	}

	for name, tc := range cases {
		suite.Run(name, func() { suite.runCase(tc) })
	}
}

func (suite *DependencyGuardTestSuite) TestUndefinedReferencesAreSkipped() {
	defs := config.OptionDefinitions{testParent: {Global: true, Default: "${MISSING}"}}
	values := map[string]config.OptionValue{testParent: {Children: []string{"ghost"}}}

	g := graph.NewGraph[*types.Option]()
	suite.Require().NoError(addOptionsToGraph(g, defs, values))
	suite.False(g.HasEdge(testParent, "ghost"))
	suite.False(g.HasEdge("MISSING", testParent))
}

func (suite *DependencyGuardTestSuite) TestResolveRejectsIncompatibleGraph() {
	defs := map[string]*types.Option{testParent: {}, testChild: {Global: true}}
	values := map[string]config.OptionValue{testParent: {Children: []string{testChild}}}

	resolved, _, err := New(map[string]string{}, map[string]string{}).Resolve(
		context.Background(), nil, defs, values,
	)
	suite.Nil(resolved)
	suite.ErrorContains(err, testGlobalChildErr)
}

func (suite *DependencyGuardTestSuite) TestResolveKeepsValidBoundaryUserValue() {
	defs := map[string]*types.Option{"count": {Type: typeNumber}}

	resolved, _, err := New(
		map[string]string{"count": "-9223372036854775808"},
		map[string]string{},
	).Resolve(context.Background(), nil, defs, map[string]config.OptionValue{})
	suite.Require().NoError(err)
	suite.Equal("-9223372036854775808", resolved["count"].Value)
}

func (suite *DependencyGuardTestSuite) runCase(tc dependencyCase) {
	g := graph.NewGraph[*types.Option]()
	err := addOptionsToGraph(g, tc.defs, tc.values)
	if tc.wantErr != "" {
		suite.ErrorContains(err, tc.wantErr)
		suite.False(g.HasEdge(tc.from, tc.to))
		return
	}
	suite.Require().NoError(err)
	suite.True(g.HasEdge(tc.from, tc.to))
	suite.False(g.HasEdge(tc.to, tc.from))
}
