package resolver

import (
	"fmt"
	"maps"
	"sort"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/graph"
	"github.com/devsy-org/devsy/pkg/types"
)

func combine(
	resolvedOptions map[string]config.OptionValue,
	extraValues map[string]string,
) map[string]string {
	options := map[string]string{}
	maps.Copy(options, extraValues)
	for k, v := range resolvedOptions {
		options[k] = v.Value
	}
	return options
}

func addDependencies(
	g *graph.Graph[*types.Option],
	options config.OptionDefinitions,
	optionValues map[string]config.OptionValue,
) error {
	for optionName := range options {
		err := addDependency(g, optionValues, optionName)
		if err != nil {
			return err
		}
	}
	return nil
}

func addDependency(
	g *graph.Graph[*types.Option],
	optionValues map[string]config.OptionValue,
	optionName string,
) error {
	option, exists := g.GetNode(optionName)
	if !exists || option == nil {
		return nil
	}

	if err := addChildDependencies(
		g,
		option,
		optionName,
		optionValues[optionName].Children,
	); err != nil {
		return err
	}

	if err := addVariableDependencies(variableDependenciesParams{
		g:          g,
		option:     option,
		optionName: optionName,
		deps:       findVariables(option.Default),
		kind:       "default",
	}); err != nil {
		return err
	}

	return addVariableDependencies(variableDependenciesParams{
		g:          g,
		option:     option,
		optionName: optionName,
		deps:       findVariables(option.Command),
		kind:       "command",
	})
}

func addChildDependencies(
	g *graph.Graph[*types.Option],
	option *types.Option,
	optionName string,
	children []string,
) error {
	for _, childName := range children {
		if !g.HasNode(childName) || childName == optionName {
			continue
		}

		if err := validateChildDependency(g, option, optionName, childName); err != nil {
			return err
		}

		_ = g.AddEdge(optionName, childName)
	}

	return nil
}

func validateChildDependency(
	g *graph.Graph[*types.Option],
	option *types.Option,
	optionName, childName string,
) error {
	childOption, childExists := g.GetNode(childName)
	if !childExists || childOption == nil {
		return nil
	}

	if !option.Global && childOption.Global {
		return fmt.Errorf(
			"cannot use a global option as a dependency of a non-global option. Option %q used in children of option %q",
			childName,
			optionName,
		)
	}
	if option.Local && !childOption.Local {
		return fmt.Errorf(
			"cannot use a non-local option as a dependency of a local option. Option %q used in children of option %q",
			childName,
			optionName,
		)
	}

	return nil
}

type variableDependenciesParams struct {
	g          *graph.Graph[*types.Option]
	option     *types.Option
	optionName string
	deps       []string
	kind       string
}

func addVariableDependencies(p variableDependenciesParams) error {
	for _, dep := range p.deps {
		if !p.g.HasNode(dep) || dep == p.optionName {
			continue
		}

		if err := validateVariableDependency(p, dep); err != nil {
			return err
		}

		_ = p.g.AddEdge(dep, p.optionName)
	}

	return nil
}

func validateVariableDependency(p variableDependenciesParams, dep string) error {
	depOption, depExists := p.g.GetNode(dep)
	if !depExists || depOption == nil {
		return nil
	}

	if p.option.Global && !depOption.Global {
		return fmt.Errorf(
			"cannot use a non-global option as a dependency of a global option. Option %q used in %s of option %q",
			dep,
			p.kind,
			p.optionName,
		)
	}
	if !p.option.Local && depOption.Local {
		return fmt.Errorf(
			"cannot use a local option as a dependency of a non-local option. Option %q used in %s of option %q",
			dep,
			p.kind,
			p.optionName,
		)
	}

	return nil
}

func addOptionsToGraph(
	g *graph.Graph[*types.Option],
	optionDefinitions config.OptionDefinitions,
	optionValues map[string]config.OptionValue,
) error {
	if !g.HasNode(rootID) {
		_ = g.AddNode(rootID, nil)
	}

	for optionName, option := range optionDefinitions {
		_ = g.SetNode(optionName, option)
		_ = g.AddEdge(rootID, optionName)
	}

	err := addDependencies(g, optionDefinitions, optionValues)
	if err != nil {
		return err
	}

	return nil
}

func findVariables(str string) []string {
	retVars := map[string]bool{}
	matches := variableExpression.FindAllStringSubmatch(str, -1)
	for _, match := range matches {
		if len(match) >= 2 {
			retVars[match[1]] = true
		}
	}

	retVarsArr := []string{}
	for k := range retVars {
		retVarsArr = append(retVarsArr, k)
	}

	sort.Strings(retVarsArr)
	return retVarsArr
}

func mergeMaps[K comparable, V any](existing map[K]V, newOpts map[K]V) map[K]V {
	retOpts := map[K]V{}
	maps.Copy(retOpts, existing)
	maps.Copy(retOpts, newOpts)

	return retOpts
}
