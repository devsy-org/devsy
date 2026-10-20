package resolver

import (
	"context"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/graph"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/suite"
)

type DependencyGuardTestSuite struct {
	suite.Suite
}

func TestDependencyGuardTestSuite(t *testing.T) {
	suite.Run(t, new(DependencyGuardTestSuite))
}

// dependencyCase: wantErr is the scope error that must be returned (empty means accepted).
// from->to must be an edge when accepted and absent when rejected.
type dependencyCase struct {
	defs    config.OptionDefinitions
	values  map[string]config.OptionValue
	wantErr string
	from    string
	to      string
}

func childCase(parent, child *types.Option, wantErr string) dependencyCase {
	return dependencyCase{
		defs:    config.OptionDefinitions{"parent": parent, "child": child},
		values:  map[string]config.OptionValue{"parent": {Children: []string{"child"}}},
		wantErr: wantErr,
		from:    "parent",
		to:      "child",
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

func (suite *DependencyGuardTestSuite) TestChildScopeGuards() {
	cases := map[string]dependencyCase{
		"global child under non-global parent rejected": childCase(
			&types.Option{},
			&types.Option{Global: true},
			`cannot use a global option as a dependency of a non-global option. Option "child" used in children of option "parent"`,
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
			`cannot use a non-local option as a dependency of a local option. Option "child" used in children of option "parent"`,
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
			&types.Option{Global: true, Default: "${V}"},
			&types.Option{},
			`cannot use a non-global option as a dependency of a global option. Option "V" used in default of option "opt"`,
		),
		"global default referencing global variable accepted": variableCase(
			&types.Option{Global: true, Default: "${V}"}, &types.Option{Global: true}, "",
		),
		"non-local command referencing local variable rejected": variableCase(
			&types.Option{Command: "echo ${V}"},
			&types.Option{Local: true},
			`cannot use a local option as a dependency of a non-local option. Option "V" used in command of option "opt"`,
		),
		"local default referencing non-local variable accepted": variableCase(
			&types.Option{Local: true, Default: "${V}"}, &types.Option{}, "",
		),
	}

	for name, tc := range cases {
		suite.Run(name, func() { suite.runCase(tc) })
	}
}

func (suite *DependencyGuardTestSuite) TestUndefinedReferencesAreSkipped() {
	defs := config.OptionDefinitions{"parent": {Global: true, Default: "${MISSING}"}}
	values := map[string]config.OptionValue{"parent": {Children: []string{"ghost"}}}

	g := graph.NewGraph[*types.Option]()
	suite.Require().NoError(addOptionsToGraph(g, defs, values))
	suite.False(g.HasEdge("parent", "ghost"))
	suite.False(g.HasEdge("MISSING", "parent"))
}

func (suite *DependencyGuardTestSuite) TestResolveRejectsIncompatibleGraph() {
	defs := map[string]*types.Option{"parent": {}, "child": {Global: true}}
	values := map[string]config.OptionValue{"parent": {Children: []string{"child"}}}

	resolved, _, err := New(map[string]string{}, map[string]string{}).Resolve(
		context.Background(), nil, defs, values,
	)
	suite.Nil(resolved)
	suite.ErrorContains(
		err,
		`cannot use a global option as a dependency of a non-global option. Option "child" used in children of option "parent"`,
	)
}

func (suite *DependencyGuardTestSuite) TestResolveKeepsValidBoundaryUserValue() {
	defs := map[string]*types.Option{"count": {Type: "number"}}

	resolved, _, err := New(
		map[string]string{"count": "-9223372036854775808"},
		map[string]string{},
	).Resolve(context.Background(), nil, defs, map[string]config.OptionValue{})
	suite.Require().NoError(err)
	suite.Equal("-9223372036854775808", resolved["count"].Value)
}
