package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

type ginkgoSuiteConfigReport struct {
	DryRun *bool `json:"DryRun"`
}

type ginkgoSuiteReport struct {
	SuiteConfig               *ginkgoSuiteConfigReport `json:"SuiteConfig"`
	SuiteSucceeded            *bool                    `json:"SuiteSucceeded"`
	SuiteHasProgrammaticFocus *bool                    `json:"SuiteHasProgrammaticFocus"`
	SpecReports               []json.RawMessage        `json:"SpecReports"`
}

type ginkgoSpecReport struct {
	ContainerHierarchyLabels json.RawMessage `json:"ContainerHierarchyLabels"`
	LeafNodeLabels           json.RawMessage `json:"LeafNodeLabels"`
	LeafNodeType             string          `json:"LeafNodeType"`
	State                    string          `json:"State"`
	NumAttempts              int             `json:"NumAttempts"`
}

type selectedGinkgoSpec struct {
	report ginkgoSpecReport
	labels map[string]struct{}
}

const (
	providerModeParity   = "parity"
	providerModeExternal = "external"
)

func validateReport(path, label string, expectedCount int, mode string) error {
	if !validProviderExpectation(mode, expectedCount) {
		return fmt.Errorf("expected-count %d is invalid for provider-mode %q", expectedCount, mode)
	}
	specs, err := readSuiteSpecs(path)
	if err != nil {
		return err
	}
	selected, err := selectLabeledSpecs(specs, label, expectedCount)
	if err != nil {
		return err
	}
	return validateSelectedSpecs(selected, expectedCount, mode)
}

func validProviderExpectation(mode string, expectedCount int) bool {
	switch mode {
	case providerModeParity:
		return isPositiveEven(expectedCount)
	case providerModeExternal:
		return expectedCount > 0
	default:
		return false
	}
}

func selectLabeledSpecs(
	specs []json.RawMessage,
	label string,
	capacity int,
) ([]selectedGinkgoSpec, error) {
	selected := make([]selectedGinkgoSpec, 0, capacity)
	for index, rawSpec := range specs {
		item, err := decodeSelectedSpec(rawSpec, index)
		if err != nil {
			return nil, err
		}
		if _, ok := item.labels[label]; ok {
			selected = append(selected, item)
		}
	}
	if len(selected) != capacity {
		return nil, fmt.Errorf(
			"label %q: expected %d specs, found %d",
			label,
			capacity,
			len(selected),
		)
	}
	return selected, nil
}

func decodeSelectedSpec(raw json.RawMessage, index int) (selectedGinkgoSpec, error) {
	if !isJSONKind(raw, '{') {
		return selectedGinkgoSpec{}, fmt.Errorf(
			"report contains a malformed spec entry at index %d",
			index,
		)
	}
	var spec ginkgoSpecReport
	if err := json.Unmarshal(raw, &spec); err != nil {
		return selectedGinkgoSpec{}, fmt.Errorf(
			"report contains a malformed spec entry at index %d: %w",
			index,
			err,
		)
	}
	labels, err := specLabels(spec)
	if err != nil {
		return selectedGinkgoSpec{}, fmt.Errorf(
			"report contains a malformed spec entry at index %d: %w",
			index,
			err,
		)
	}
	return selectedGinkgoSpec{report: spec, labels: labels}, nil
}

func readSuiteSpecs(path string) ([]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read Ginkgo JSON report: %w", err)
	}
	if err := requireReportArray(data); err != nil {
		return nil, err
	}
	var reports []json.RawMessage
	if err := json.Unmarshal(data, &reports); err != nil {
		return nil, fmt.Errorf("cannot read Ginkgo JSON report: %w", err)
	}
	if len(reports) != 1 || !isJSONKind(reports[0], '{') {
		return nil, errors.New("expected one Ginkgo suite report")
	}
	return parseSuiteSpecs(reports[0])
}

func requireReportArray(data []byte) error {
	if isJSONKind(data, '[') {
		return nil
	}
	if !json.Valid(data) {
		return fmt.Errorf("cannot read Ginkgo JSON report: %w", json.Unmarshal(data, new(any)))
	}
	return errors.New("expected one Ginkgo suite report")
}

func parseSuiteSpecs(data json.RawMessage) ([]json.RawMessage, error) {
	var suite ginkgoSuiteReport
	if err := json.Unmarshal(data, &suite); err != nil {
		return nil, fmt.Errorf("suite report is malformed: %w", err)
	}
	if err := validateSuiteExecution(suite); err != nil {
		return nil, err
	}
	if suite.SpecReports == nil {
		return nil, errors.New("report has no spec list")
	}
	return suite.SpecReports, nil
}

func validateSuiteExecution(suite ginkgoSuiteReport) error {
	if suite.SuiteConfig == nil || suite.SuiteConfig.DryRun == nil || *suite.SuiteConfig.DryRun {
		return errors.New("report does not prove a real test run")
	}
	if suite.SuiteSucceeded == nil || !*suite.SuiteSucceeded {
		return errors.New("suite did not succeed")
	}
	if suite.SuiteHasProgrammaticFocus == nil || *suite.SuiteHasProgrammaticFocus {
		return errors.New("report has programmatic focus")
	}
	return nil
}

func validateSelectedSpecs(selected []selectedGinkgoSpec, expectedCount int, mode string) error {
	providers := [2]int{}
	for index, item := range selected {
		provider, err := validateSelectedSpec(item, index)
		if err != nil {
			return err
		}
		providers[provider]++
	}
	if mode == providerModeExternal {
		if providers[0] != 0 || providers[1] != expectedCount {
			return fmt.Errorf(
				"provider label counts (builtin=%d, external=%d), expected all %d external",
				providers[0],
				providers[1],
				expectedCount,
			)
		}
		return nil
	}
	if providers[0] != expectedCount/2 || providers[1] != expectedCount/2 {
		return fmt.Errorf(
			"provider label counts (builtin=%d, external=%d), expected %d each",
			providers[0],
			providers[1],
			expectedCount/2,
		)
	}
	return nil
}

func validateSelectedSpec(item selectedGinkgoSpec, index int) (int, error) {
	builtin := hasLabel(item.labels, "microsandbox-builtin")
	external := hasLabel(item.labels, "microsandbox-external")
	if builtin == external {
		return 0, fmt.Errorf("selected spec %d must have exactly one provider label", index+1)
	}
	if item.report.LeafNodeType != "It" {
		return 0, fmt.Errorf("selected report %d is not an It spec", index+1)
	}
	if item.report.State != "passed" {
		return 0, fmt.Errorf("selected spec %d state is %q, not passed", index+1, item.report.State)
	}
	if item.report.NumAttempts != 1 {
		return 0, fmt.Errorf(
			"selected spec %d used %d attempts, expected one",
			index+1,
			item.report.NumAttempts,
		)
	}
	if external {
		return 1, nil
	}
	return 0, nil
}

func hasLabel(labels map[string]struct{}, label string) bool {
	_, ok := labels[label]
	return ok
}

func specLabels(spec ginkgoSpecReport) (map[string]struct{}, error) {
	leaves, err := decodeStringLabels(spec.LeafNodeLabels)
	if err != nil {
		return nil, err
	}
	groups, err := decodeLabelGroups(spec.ContainerHierarchyLabels)
	if err != nil {
		return nil, err
	}
	labels := make(map[string]struct{}, len(leaves))
	for _, label := range leaves {
		labels[label] = struct{}{}
	}
	for _, hierarchy := range groups {
		for _, label := range hierarchy {
			labels[label] = struct{}{}
		}
	}
	return labels, nil
}

func decodeStringLabels(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || isJSONNull(raw) {
		return nil, nil
	}
	if !isJSONKind(raw, '[') {
		return nil, errors.New("label field is not a string list")
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	labels := make([]string, 0, len(values))
	for _, value := range values {
		if !isJSONKind(value, '"') {
			return nil, errors.New("label list contains a non-string value")
		}
		var label string
		if err := json.Unmarshal(value, &label); err != nil {
			return nil, err
		}
		labels = append(labels, label)
	}
	return labels, nil
}

func decodeLabelGroups(raw json.RawMessage) ([][]string, error) {
	if len(raw) == 0 || isJSONNull(raw) {
		return nil, nil
	}
	if !isJSONKind(raw, '[') {
		return nil, errors.New("container hierarchy labels are not a list")
	}
	var groups []json.RawMessage
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, err
	}
	labels := make([][]string, 0, len(groups))
	for _, group := range groups {
		if !isJSONKind(group, '[') {
			return nil, errors.New("container hierarchy contains a non-list group")
		}
		decoded, err := decodeStringLabels(group)
		if err != nil {
			return nil, fmt.Errorf("container hierarchy group: %w", err)
		}
		labels = append(labels, decoded)
	}
	return labels, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func isJSONKind(data []byte, kind byte) bool {
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) > 0 && trimmed[0] == kind
}
