package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	skippedState          = "skipped"
	reportTestLabel       = "up-provider-microsandbox-images"
	numAttemptsKey        = "NumAttempts"
	stateKey              = "State"
	leafNodeTypeKey       = "LeafNodeType"
	exactlyOneProviderErr = "exactly one provider label"
	realRunError          = "real test run"
	focusError            = "programmatic focus"
	builtinProviderLabel  = "microsandbox-builtin"
	externalProviderLabel = "microsandbox-external"
)

func TestValidateReportAcceptsExactSuccessfulProviderMatrix(t *testing.T) {
	path := writeReportFixture(t, validReportFor(reportTestLabel))
	if err := validateReport(path, reportTestLabel, 2, providerModeParity); err != nil {
		t.Fatal(err)
	}
}

func TestValidateReportAllowsUnselectedSkippedSpecs(t *testing.T) {
	report := validReportFor(reportTestLabel)
	suite := report[0].(map[string]any)
	specs := suite["SpecReports"].([]any)
	specs = append(specs, map[string]any{stateKey: skippedState, numAttemptsKey: 0})
	suite["SpecReports"] = specs
	if err := validateReport(
		writeReportFixture(t, report),
		reportTestLabel,
		2,
		providerModeParity,
	); err != nil {
		t.Fatal(err)
	}
}

func TestValidateReportRejectsMissingMalformedAndWrongRoot(t *testing.T) {
	directory := t.TempDir()
	assertReportErrorContains(t, filepath.Join(directory, "missing.json"), "cannot read")
	malformed := filepath.Join(directory, "malformed.json")
	if err := os.WriteFile(malformed, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertReportErrorContains(t, malformed, "cannot read")
	for _, value := range []string{`{}`, `[]`, `[{},{}]`, `[null]`} {
		path := filepath.Join(directory, "shape.json")
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		assertReportErrorContains(t, path, "expected one Ginkgo suite")
	}
}

func assertReportErrorContains(t *testing.T, path, want string) {
	t.Helper()
	err := validateReport(path, reportTestLabel, 2, providerModeParity)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("validateReport error = %v, want %q", err, want)
	}
}

func TestValidateReportRequiresExplicitSuiteEvidence(t *testing.T) {
	cases := []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"missing config", func(s map[string]any) { delete(s, "SuiteConfig") }, realRunError},
		{
			"missing dry run field",
			func(s map[string]any) { delete(s["SuiteConfig"].(map[string]any), "DryRun") },
			realRunError,
		},
		{
			"dry run",
			func(s map[string]any) { s["SuiteConfig"].(map[string]any)["DryRun"] = true },
			realRunError,
		},
		{
			"missing suite success",
			func(s map[string]any) { delete(s, "SuiteSucceeded") },
			"suite did not succeed",
		},
		{
			"failed suite",
			func(s map[string]any) { s["SuiteSucceeded"] = false },
			"suite did not succeed",
		},
		{
			"missing focus field",
			func(s map[string]any) { delete(s, "SuiteHasProgrammaticFocus") },
			focusError,
		},
		{focusError, func(s map[string]any) { s["SuiteHasProgrammaticFocus"] = true }, focusError},
		{"missing spec list", func(s map[string]any) { delete(s, "SpecReports") }, "no spec list"},
		{"null spec list", func(s map[string]any) { s["SpecReports"] = nil }, "no spec list"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			report := validReportFor(reportTestLabel)
			test.edit(report[0].(map[string]any))
			err := validateReport(
				writeReportFixture(t, report),
				reportTestLabel,
				2,
				providerModeParity,
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateReport() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateReportRejectsMalformedSpecRecords(t *testing.T) {
	for _, mutate := range []func(map[string]any){
		func(s map[string]any) { s["LeafNodeLabels"] = "not-a-list" },
		func(s map[string]any) { s["LeafNodeLabels"] = []any{externalProviderLabel, nil} },
		func(s map[string]any) { s["ContainerHierarchyLabels"] = []any{"not-a-label-list"} },
		func(s map[string]any) { s["ContainerHierarchyLabels"] = []any{nil} },
		func(s map[string]any) { s["ContainerHierarchyLabels"] = []any{[]any{"suite-label", nil}} },
		func(s map[string]any) { s[leafNodeTypeKey] = 7 },
	} {
		report := validReportFor(reportTestLabel)
		mutate(report[0].(map[string]any)["SpecReports"].([]any)[0].(map[string]any))
		requireReportFailure(t, report, "malformed spec")
	}
	path := writeReportFixture(t, []any{map[string]any{
		"SuiteConfig": map[string]any{"DryRun": false}, "SuiteSucceeded": true,
		"SuiteHasProgrammaticFocus": false, "SpecReports": []any{nil},
	}})
	if err := validateReport(
		path,
		reportTestLabel,
		2,
		providerModeParity,
	); err == nil ||
		!strings.Contains(err.Error(), "malformed spec") {
		t.Fatalf("non-object spec error = %v", err)
	}
}

func TestValidateReportRejectsSkippedPendingAndFailedSelectedSpecs(t *testing.T) {
	for _, state := range []string{skippedState, "pending", "failed"} {
		t.Run(state, func(t *testing.T) {
			report := validReportFor(reportTestLabel)
			report[0].(map[string]any)["SpecReports"].([]any)[0].(map[string]any)[stateKey] = state
			requireReportFailure(t, report, "not passed")
		})
	}
}

func TestValidateReportRejectsRetryAndNonItSelectedSpec(t *testing.T) {
	for name, field := range map[string]struct {
		key   string
		value any
		want  string
	}{
		"retry":     {key: numAttemptsKey, value: 2, want: "expected one"},
		"not an It": {key: leafNodeTypeKey, value: "ReportEntry", want: "not an It spec"},
	} {
		t.Run(name, func(t *testing.T) {
			report := validReportFor(reportTestLabel)
			report[0].(map[string]any)["SpecReports"].([]any)[0].(map[string]any)[field.key] = field.value
			requireReportFailure(t, report, field.want)
		})
	}
}

func TestValidateReportRejectsWrongLabelAndCount(t *testing.T) {
	path := writeReportFixture(t, validReportFor(reportTestLabel))
	for _, test := range []struct {
		label string
		count int
		want  string
	}{
		{label: "up-provider-microsandbox-storage", count: 2, want: "found 0"},
		{label: reportTestLabel, count: 4, want: "expected 4 specs, found 2"},
	} {
		if err := validateReport(
			path,
			test.label,
			test.count,
			providerModeParity,
		); err == nil ||
			!strings.Contains(err.Error(), test.want) {
			t.Errorf("validateReport(%q, %d) = %v", test.label, test.count, err)
		}
	}
}

func TestValidateReportRejectsProviderCoverageErrors(t *testing.T) {
	cases := []struct {
		name   string
		labels []string
		want   string
	}{
		{"missing", nil, exactlyOneProviderErr},
		{
			"ambiguous",
			[]string{builtinProviderLabel, externalProviderLabel},
			exactlyOneProviderErr,
		},
		{"unbalanced", []string{builtinProviderLabel}, "provider label counts"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			report := validReportFor(reportTestLabel)
			report[0].(map[string]any)["SpecReports"].([]any)[1].(map[string]any)["LeafNodeLabels"] = test.labels
			requireReportFailure(t, report, test.want)
		})
	}
}

func requireReportFailure(t *testing.T, report any, want string) {
	t.Helper()
	err := validateReport(writeReportFixture(t, report), reportTestLabel, 2, providerModeParity)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("validateReport error = %v, want %q", err, want)
	}
}

func TestValidateReportRequiresPositiveEvenCount(t *testing.T) {
	for _, count := range []int{0, -2, 1, 3} {
		if err := validateReport(
			"",
			reportTestLabel,
			count,
			providerModeParity,
		); err == nil ||
			!strings.Contains(err.Error(), "invalid") {
			t.Errorf("expected count %d error = %v", count, err)
		}
	}
}

func TestValidateReportAcceptsThreeExternalSpecs(t *testing.T) {
	path := writeReportFixture(t, validExternalReportFor(reportTestLabel))
	if err := validateReport(path, reportTestLabel, 3, providerModeExternal); err != nil {
		t.Fatal(err)
	}
}

func TestValidateReportExternalModeRejectsProviderLabelErrors(t *testing.T) {
	cases := []struct {
		name   string
		labels []string
		want   string
	}{
		{"builtin", []string{builtinProviderLabel}, "expected all 3 external"},
		{"mixed", []string{builtinProviderLabel}, "expected all 3 external"},
		{"missing", nil, exactlyOneProviderErr},
		{
			"ambiguous",
			[]string{builtinProviderLabel, externalProviderLabel},
			exactlyOneProviderErr,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			report := validExternalReportFor(reportTestLabel)
			specs := report[0].(map[string]any)["SpecReports"].([]any)
			if test.name == "builtin" {
				for _, raw := range specs {
					raw.(map[string]any)["LeafNodeLabels"] = test.labels
				}
			} else {
				specs[0].(map[string]any)["LeafNodeLabels"] = test.labels
			}
			err := validateReport(
				writeReportFixture(t, report),
				reportTestLabel,
				3,
				providerModeExternal,
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateReport() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateReportExternalModeRejectsSkippedOrRetriedSpecs(t *testing.T) {
	for _, test := range []struct {
		name  string
		field string
		value any
		want  string
	}{
		{skippedState, stateKey, skippedState, "not passed"},
		{"retried", numAttemptsKey, 2, "expected one"},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := validExternalReportFor(reportTestLabel)
			selected := report[0].(map[string]any)["SpecReports"].([]any)[0].(map[string]any)
			selected[test.field] = test.value
			err := validateReport(
				writeReportFixture(t, report),
				reportTestLabel,
				3,
				providerModeExternal,
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateReport() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateReportRejectsInvalidProviderModeAndCount(t *testing.T) {
	path := writeReportFixture(t, validExternalReportFor(reportTestLabel))
	for _, test := range []struct {
		mode  string
		count int
	}{
		{providerModeParity, 3},
		{providerModeExternal, 0},
		{unknownProviderMode, 3},
	} {
		if err := validateReport(path, reportTestLabel, test.count, test.mode); err == nil {
			t.Errorf("validateReport(%q, %d) unexpectedly succeeded", test.mode, test.count)
		}
	}
}

func writeReportFixture(t *testing.T, report any) string {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validReportFor(label string) []any {
	providers := []string{builtinProviderLabel, externalProviderLabel}
	specs := make([]any, 2)
	for index := range specs {
		specs[index] = map[string]any{
			"ContainerHierarchyLabels": [][]string{{label}},
			"LeafNodeLabels":           []string{providers[index%len(providers)]},
			leafNodeTypeKey:            "It",
			stateKey:                   "passed",
			numAttemptsKey:             1,
		}
	}
	return []any{map[string]any{
		"SuiteConfig":               map[string]any{"DryRun": false},
		"SuiteSucceeded":            true,
		"SuiteHasProgrammaticFocus": false,
		"SpecReports":               specs,
	}}
}

func validExternalReportFor(label string) []any {
	report := validReportFor(label)
	suite := report[0].(map[string]any)
	specs := suite["SpecReports"].([]any)
	specs = append(specs, map[string]any{
		"ContainerHierarchyLabels": [][]string{{label}},
		"LeafNodeLabels":           []string{externalProviderLabel},
		leafNodeTypeKey:            "It",
		stateKey:                   "passed",
		numAttemptsKey:             1,
	})
	specs[0].(map[string]any)["LeafNodeLabels"] = []string{externalProviderLabel}
	specs[1].(map[string]any)["LeafNodeLabels"] = []string{externalProviderLabel}
	suite["SpecReports"] = specs
	return report
}
