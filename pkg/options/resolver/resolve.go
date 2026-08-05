package resolver

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/survey"
	"github.com/devsy-org/devsy/pkg/terminal"
	"github.com/devsy-org/devsy/pkg/types"
)

func (r *Resolver) resolveOptions(
	ctx context.Context,
	optionValues map[string]config.OptionValue,
) (map[string]config.OptionValue, error) {
	resolvedOptionValues := map[string]config.OptionValue{}
	maps.Copy(resolvedOptionValues, optionValues)

	sortedOptionNames, err := r.graph.SortNodeIDs()
	if err != nil {
		return nil, fmt.Errorf("failed to sort options: %w", err)
	}

	for _, optionName := range sortedOptionNames {
		if optionName == rootID {
			continue
		}

		if !r.graph.HasNode(optionName) {
			continue
		}

		err := r.resolveOption(ctx, optionName, resolvedOptionValues)
		if err != nil {
			return nil, fmt.Errorf("resolve option %s: %w", optionName, err)
		}

		err = r.refreshSubOptions(ctx, optionName, resolvedOptionValues)
		if err != nil {
			return nil, fmt.Errorf("refresh sub options for %s: %w", optionName, err)
		}
	}

	return resolvedOptionValues, nil
}

func (r *Resolver) resolveOption(
	ctx context.Context,
	optionName string,
	resolvedOptionValues map[string]config.OptionValue,
) error {
	option, exists := r.graph.GetNode(optionName)
	if !exists {
		return fmt.Errorf("option %s not found in graph", optionName)
	}

	// get existing values
	existing, err := r.getValue(
		optionName,
		option,
		resolvedOptionValues,
	)
	if err != nil {
		return err
	}

	skip, err := r.shouldSkipResolve(skipResolveParams{
		optionName:    optionName,
		option:        option,
		userValueOk:   existing.userValueOk,
		beforeValue:   existing.beforeValue,
		beforeValueOk: existing.beforeValueOk,
	})
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	if err := r.computeOptionValue(ctx, computeOptionValueParams{
		optionName:           optionName,
		option:               option,
		userValue:            existing.userValue,
		userValueOk:          existing.userValueOk,
		beforeValue:          existing.beforeValue,
		resolvedOptionValues: resolvedOptionValues,
	}); err != nil {
		return err
	}

	if err := r.resolveRequired(
		optionName,
		option,
		existing.userValueOk,
		resolvedOptionValues,
	); err != nil {
		return err
	}

	r.invalidateChangedChildren(optionName, existing.beforeValue, resolvedOptionValues)

	return nil
}

type skipResolveParams struct {
	optionName    string
	option        *types.Option
	userValueOk   bool
	beforeValue   config.OptionValue
	beforeValueOk bool
}

func (r *Resolver) shouldSkipResolve(p skipResolveParams) (bool, error) {
	if p.userValueOk {
		return false, nil
	}

	if p.beforeValueOk {
		skip, err := beforeValueStillValid(p.optionName, p.option, p.beforeValue)
		if err != nil {
			return false, err
		}
		if skip {
			return true, nil
		}
	}

	return r.skipForScope(p.option), nil
}

func beforeValueStillValid(
	optionName string,
	option *types.Option,
	beforeValue config.OptionValue,
) (bool, error) {
	if beforeValue.UserProvided || option.Cache == "" {
		return true, nil
	}

	duration, err := time.ParseDuration(option.Cache)
	if err != nil {
		return false, fmt.Errorf("parse cache duration of option %s: %w", optionName, err)
	}

	// has value expired?
	if beforeValue.Filled != nil && beforeValue.Filled.Add(duration).After(time.Now()) {
		return true, nil
	}

	return false, nil
}

func (r *Resolver) skipForScope(option *types.Option) bool {
	// make sure required is always resolved
	if option.Required {
		return false
	}
	if !r.resolveGlobal && option.Global {
		return true
	}
	if !r.resolveLocal && option.Local {
		return true
	}

	return false
}

type computeOptionValueParams struct {
	optionName           string
	option               *types.Option
	userValue            string
	userValueOk          bool
	beforeValue          config.OptionValue
	resolvedOptionValues map[string]config.OptionValue
}

func (r *Resolver) computeOptionValue(ctx context.Context, p computeOptionValueParams) error {
	switch {
	case p.userValueOk:
		p.resolvedOptionValues[p.optionName] = config.OptionValue{
			Value:        p.userValue,
			Children:     p.beforeValue.Children,
			UserProvided: true,
		}
	case p.option.Default != "":
		p.resolvedOptionValues[p.optionName] = config.OptionValue{
			Children: p.beforeValue.Children,
			Value: ResolveDefaultValue(
				p.option.Default,
				combine(p.resolvedOptionValues, r.extraValues),
			),
		}
	case p.option.Command != "":
		optionValue, err := resolveFromCommand(ctx, p.option, p.resolvedOptionValues, r.extraValues)
		if err != nil {
			return err
		}

		optionValue.Children = p.beforeValue.Children
		p.resolvedOptionValues[p.optionName] = optionValue
	case len(p.option.Enum) == 1:
		p.resolvedOptionValues[p.optionName] = config.OptionValue{
			Children: p.beforeValue.Children,
			Value:    p.option.Enum[0].Value,
		}
	default:
		p.resolvedOptionValues[p.optionName] = config.OptionValue{
			Children: p.beforeValue.Children,
		}
	}

	return nil
}

func (r *Resolver) resolveRequired(
	optionName string,
	option *types.Option,
	userValueOk bool,
	resolvedOptionValues map[string]config.OptionValue,
) error {
	if userValueOk || !option.Required {
		return nil
	}

	current := resolvedOptionValues[optionName]
	if current.Value != "" || current.UserProvided {
		return nil
	}

	if r.skipRequired {
		delete(resolvedOptionValues, optionName)
		return r.graph.RemoveChildren(optionName)
	}

	return r.askRequired(optionName, option, resolvedOptionValues)
}

func (r *Resolver) askRequired(
	optionName string,
	option *types.Option,
	resolvedOptionValues map[string]config.OptionValue,
) error {
	// check if we can ask a question
	if !terminal.IsTerminalIn {
		return fmt.Errorf("option %s is required, but no value provided", optionName)
	}

	questionOpts := []string{}
	for _, enumOpt := range option.Enum {
		questionOpts = append(questionOpts, enumOpt.Value)
	}

	// check if there is only one option
	log.Info(option.Description)
	answer, err := log.QuestionDefault(&survey.QuestionOptions{
		Question:               fmt.Sprintf("Enter a value for %s", optionName),
		Options:                questionOpts,
		ValidationRegexPattern: option.ValidationPattern,
		ValidationMessage:      option.ValidationMessage,
		IsPassword:             option.Password,
	})
	if err != nil {
		return err
	}

	resolvedOptionValues[optionName] = config.OptionValue{
		Value:        answer,
		UserProvided: true,
	}

	return nil
}

func (r *Resolver) invalidateChangedChildren(
	optionName string,
	beforeValue config.OptionValue,
	resolvedOptionValues map[string]config.OptionValue,
) {
	if beforeValue.Value == resolvedOptionValues[optionName].Value {
		return
	}

	for _, childID := range r.graph.GetChildren(optionName) {
		optionValue, ok := resolvedOptionValues[childID]
		if ok && !optionValue.UserProvided {
			delete(resolvedOptionValues, childID)
		}
	}
}

// existingOptionValue bundles the user-provided and previously-resolved
// values getValue looks up for an option, along with whether each was
// present.
type existingOptionValue struct {
	userValue     string
	userValueOk   bool
	beforeValue   config.OptionValue
	beforeValueOk bool
}

func (r *Resolver) getValue(
	optionName string,
	option *types.Option,
	resolvedOptionValues map[string]config.OptionValue,
) (existingOptionValue, error) {
	// check if user value exists
	userValue, userValueOk := r.userOptions[optionName]

	// get before value
	beforeValue, beforeValueOk := resolvedOptionValues[optionName]

	// validate user value if we have one
	if userValueOk {
		err := validateUserValue(optionName, userValue, option)
		if err != nil {
			return existingOptionValue{}, err
		}
	}

	// validate existing value
	if beforeValueOk {
		err := validateUserValue(optionName, beforeValue.Value, option)
		if err != nil {
			// strip before value
			delete(resolvedOptionValues, optionName)
			beforeValue = config.OptionValue{}
			beforeValueOk = false
		}
	}

	return existingOptionValue{
		userValue:     userValue,
		userValueOk:   userValueOk,
		beforeValue:   beforeValue,
		beforeValueOk: beforeValueOk,
	}, nil
}

func (r *Resolver) refreshSubOptions(
	ctx context.Context,
	optionName string,
	resolvedOptionValues map[string]config.OptionValue,
) error {
	option, ok := r.graph.GetNode(optionName)
	if !ok {
		return nil
	}

	if !r.resolveSubOptions || option.SubOptionsCommand == "" {
		return nil
	}

	_, ok = resolvedOptionValues[optionName]
	if !ok {
		return nil
	}

	// execute the command
	newDynamicOptions, err := resolveSubOptions(ctx, option, resolvedOptionValues, r.extraValues)
	if err != nil {
		return err
	}

	r.pruneChangedChildren(optionName, newDynamicOptions, resolvedOptionValues)
	r.dropInvalidUserValues(newDynamicOptions)
	setChildren(optionName, newDynamicOptions, resolvedOptionValues)

	if err := addOptionsToGraph(r.graph, newDynamicOptions, resolvedOptionValues); err != nil {
		return fmt.Errorf("add sub options: %w", err)
	}

	if err := resolveDynamicOptions(ctx, newDynamicOptions, r, resolvedOptionValues); err != nil {
		return fmt.Errorf("resolve dynamic sub options: %w", err)
	}

	return nil
}

func (r *Resolver) pruneChangedChildren(
	optionName string,
	newOptions config.OptionDefinitions,
	values map[string]config.OptionValue,
) {
	for childID := range r.getChangedOptions(r.dynamicOptionsForNode(values[optionName].Children), newOptions, values) {
		delete(values, childID)
		_ = r.graph.RemoveNode(childID)
	}
}

func (r *Resolver) dropInvalidUserValues(newOptions config.OptionDefinitions) {
	for name, option := range newOptions {
		userValue, ok := r.userOptions[name]
		if !ok {
			continue
		}

		if err := validateUserValue(name, userValue, option); err != nil {
			delete(r.userOptions, name)
		}
	}
}

func setChildren(
	optionName string,
	newOptions config.OptionDefinitions,
	values map[string]config.OptionValue,
) {
	val := values[optionName]
	val.Children = []string{}
	for k := range newOptions {
		val.Children = append(val.Children, k)
	}
	values[optionName] = val
}

type queue struct {
	items []string
	head  int
}

func newQueue(capacity int) *queue {
	return &queue{
		items: make([]string, 0, capacity),
		head:  0,
	}
}

func (q *queue) enqueue(item string) {
	q.items = append(q.items, item)
}

func (q *queue) dequeue() string {
	if q.head >= len(q.items) {
		return ""
	}
	item := q.items[q.head]
	q.head++
	return item
}

func (q *queue) isEmpty() bool {
	return q.head >= len(q.items)
}

func resolveDynamicOptions(
	ctx context.Context,
	options config.OptionDefinitions,
	r *Resolver,
	optionValues map[string]config.OptionValue,
) error {
	q := newQueue(len(options))
	processed := make(map[string]bool)

	for optionName := range options {
		q.enqueue(optionName)
	}

	for !q.isEmpty() {
		opt := q.dequeue()

		if processed[opt] {
			continue
		}

		if !r.graph.HasNode(opt) {
			continue
		}

		err := r.resolveOption(ctx, opt, optionValues)
		if err != nil {
			return fmt.Errorf("resolve dynamic option %s: %w", opt, err)
		}

		subOptions, err := r.retrieveSubOptions(ctx, opt, optionValues)
		if err != nil {
			return fmt.Errorf("get sub options for %s: %w", opt, err)
		}

		processed[opt] = true

		enqueueUnprocessed(q, subOptions, processed)
	}
	return nil
}

func enqueueUnprocessed(q *queue, subOptions config.OptionDefinitions, processed map[string]bool) {
	for optionName := range subOptions {
		if !processed[optionName] {
			q.enqueue(optionName)
		}
	}
}

func (r *Resolver) retrieveSubOptions(
	ctx context.Context,
	optionName string,
	options map[string]config.OptionValue,
) (config.OptionDefinitions, error) {
	option, ok := r.graph.GetNode(optionName)
	if !ok || !r.resolveSubOptions || option.SubOptionsCommand == "" {
		return nil, nil
	}

	_, ok = options[optionName]
	if !ok {
		return nil, nil
	}

	suboptions, err := resolveSubOptions(ctx, option, options, r.extraValues)
	if err != nil {
		return nil, err
	}

	r.pruneChangedChildren(optionName, suboptions, options)
	r.dropInvalidUserValues(suboptions)
	setChildren(optionName, suboptions, options)

	if err := addOptionsToGraph(r.graph, suboptions, options); err != nil {
		return nil, fmt.Errorf("add sub options: %w", err)
	}

	return suboptions, nil
}

func (r *Resolver) getChangedOptions(
	oldOptions config.OptionDefinitions,
	newOptions config.OptionDefinitions,
	resolvedOptionValues map[string]config.OptionValue,
) config.OptionDefinitions {
	changedOptions := config.OptionDefinitions{}
	for oldK, oldV := range oldOptions {
		if _, ok := newOptions[oldK]; !ok {
			changedOptions[oldK] = oldV
		}
	}

	for newK, newV := range newOptions {
		if optionChanged(oldOptions, newK, newV, resolvedOptionValues) {
			changedOptions[newK] = newV
		}
	}

	return changedOptions
}

func optionChanged(
	oldOptions config.OptionDefinitions,
	newK string,
	newV *types.Option,
	resolvedOptionValues map[string]config.OptionValue,
) bool {
	oldV, ok := oldOptions[newK]
	if !ok {
		return true
	}

	oldValue, oldValueOk := resolvedOptionValues[newK]
	if !oldValueOk {
		return true
	}

	enumValues := []string{}
	for _, o := range newV.Enum {
		enumValues = append(enumValues, o.Value)
	}

	// check if value still valid
	if len(newV.Enum) > 0 && !contains(enumValues, oldValue.Value) {
		return true
	}

	// check if default has changed
	return !oldValue.UserProvided && oldV.Default != newV.Default
}

func (r *Resolver) dynamicOptionsForNode(children []string) config.OptionDefinitions {
	retValues := config.OptionDefinitions{}
	for _, childID := range children {
		if option, ok := r.graph.GetNode(childID); ok {
			retValues[childID] = option
		}
	}

	return retValues
}

func contains(stack []string, k string) bool {
	return slices.Contains(stack, k)
}
