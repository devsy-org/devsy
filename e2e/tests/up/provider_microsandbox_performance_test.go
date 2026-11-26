package up

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

type probeValidationCase struct {
	name       string
	mode       string
	supervisor string
	mutate     func(map[string]any)
	malformed  bool
	wantError  string
}

func TestProbeExpectationsValidateSDKReports(t *testing.T) {
	runProbeValidationCases(t, append(
		append(probeMetadataCases(), probeOperationCases()...), probeProcessCases()...,
	))
}

func probeMetadataCases() []probeValidationCase {
	return append(probeIdentityCases(), probeCacheCases()...)
}

func probeIdentityCases() []probeValidationCase {
	return []probeValidationCase{
		{name: "valid direct report", mode: probeModeDirect},
		{name: "valid supervised report", mode: probeModeSupervised, supervisor: "supervisor-hash"},
		{
			name:      "malformed JSON",
			mode:      probeModeDirect,
			malformed: true,
			wantError: "decode probe JSON",
		},
		{
			name:      "wrong runtime checksum",
			mode:      probeModeDirect,
			mutate:    func(r map[string]any) { r["binary_sha256"] = "other-hash" },
			wantError: "expected binary",
		},
		{
			name:      "wrong launch mode",
			mode:      probeModeDirect,
			mutate:    func(r map[string]any) { r["launch_mode"] = probeModeSupervised },
			wantError: "launch mode",
		},
		{
			name:      "unsupported expected launch mode",
			mode:      "other",
			wantError: "launch mode is unsupported",
		},
		{
			name: "wrong platform", mode: probeModeDirect,
			mutate: func(r map[string]any) { r["goos"] = "other" }, wantError: "platform",
		},
		{
			name: "missing Go version", mode: probeModeDirect,
			mutate: func(r map[string]any) { r["go_version"] = "" }, wantError: "platform",
		},
		{
			name:       "wrong supervised helper hash",
			mode:       probeModeSupervised,
			supervisor: "supervisor-hash",
			mutate:     func(r map[string]any) { r["supervisor_sha256"] = "other-helper" },
			wantError:  "supervisor checksum",
		},
		{
			name:      "direct report with helper hash",
			mode:      probeModeDirect,
			mutate:    func(r map[string]any) { r["supervisor_sha256"] = "unexpected-helper" },
			wantError: "supervisor checksum",
		},
	}
}

func probeCacheCases() []probeValidationCase {
	return []probeValidationCase{
		{
			name:      "missing cache field",
			mode:      probeModeDirect,
			mutate:    func(r map[string]any) { delete(r, "cold_cache_measured") },
			wantError: "cache claim",
		},
		{
			name:      "true cache flag",
			mode:      probeModeDirect,
			mutate:    func(r map[string]any) { r["cold_cache_measured"] = true },
			wantError: "cache claim",
		},
		{
			name:      "ill-typed cache flag",
			mode:      probeModeDirect,
			mutate:    func(r map[string]any) { r["cold_cache_measured"] = "false" },
			wantError: "decode probe JSON",
		},
		{
			name:      "wrong tested revision",
			mode:      probeModeDirect,
			mutate:    func(r map[string]any) { r["revision"] = "different-revision" },
			wantError: "revision",
		},
	}
}

func probeOperationCases() []probeValidationCase {
	return []probeValidationCase{
		{
			name:      "wrong Info operation",
			mode:      probeModeDirect,
			mutate:    func(r map[string]any) { probeSection(r, "info")["operation"] = "RunImage" },
			wantError: "Info report",
		},
		{
			name:      "wrong warm count",
			mode:      probeModeDirect,
			mutate:    func(r map[string]any) { probeSection(r, "info")["warm_samples"] = []any{} },
			wantError: "Info report",
		},
	}
}

func probeProcessCases() []probeValidationCase {
	return []probeValidationCase{
		{
			name:      "unreaped first launch",
			mode:      probeModeDirect,
			mutate:    func(r map[string]any) { probeSection(r, "find")["first_launch"].(map[string]any)["reaped"] = false },
			wantError: "Find report",
		},
		{
			name: "unreaped warm sample", mode: probeModeDirect,
			mutate: func(r map[string]any) {
				probeSection(r, "find")["warm_samples"].([]any)[0].(map[string]any)["reaped"] = false
			},
			wantError: "process that was not reaped",
		},
	}
}

func runProbeValidationCases(t *testing.T, tests []probeValidationCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { runProbeValidationCase(t, test) })
	}
}

func runProbeValidationCase(t *testing.T, test probeValidationCase) {
	t.Helper()
	raw, err := test.reportJSON()
	if err != nil {
		t.Fatal(err)
	}
	wantSupervisor := test.supervisorHash()
	expectation := probeExpectations{
		mode: test.mode, binarySHA: "runtime-hash", supervisorSHA: wantSupervisor,
		revision: "tested-revision",
	}
	assertProbeError(t, expectation.validate(raw), test.wantError)
}

func (test probeValidationCase) reportJSON() ([]byte, error) {
	if test.malformed {
		return []byte("{"), nil
	}
	report := validProbeReport(test.mode, test.supervisor)
	if test.mutate != nil {
		test.mutate(report)
	}
	return json.Marshal(report)
}

func (test probeValidationCase) supervisorHash() string {
	if test.mode == probeModeSupervised {
		return test.supervisor
	}
	return ""
}

func assertProbeError(t *testing.T, err error, expected string) {
	t.Helper()
	if expected == "" {
		if err != nil {
			t.Fatalf("expected a valid probe report, got %v", err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), expected) {
		t.Fatalf("expected error containing %q, got %v", expected, err)
	}
}

func TestValidateSDKToolRejectsDifferentBuildSource(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	err = validateSDKTool(
		path,
		runtimeSDKModule+"/cmd/devsy-runtime-spawn-probe",
		"v1.6.0",
	)
	if err == nil {
		t.Fatal("expected the test binary to fail SDK probe provenance validation")
	}
}

func TestParseStartupEvents(t *testing.T) {
	output := strings.Join([]string{
		"DEBU External runtime Info startup completed in 4.2ms",
		"DEBU External runtime Find startup completed in 1s",
		"DEBU External runtime Info startup completed in someday",
		"DEBU External runtime Info startup completed in 0s",
		"DEBU External runtime Info startup completed in -3ms",
	}, "\n")

	events := parseStartupEvents(output)
	if len(events) != 2 {
		t.Fatalf("expected two positive startup timings, got %#v", events)
	}
	if events[0].Operation != "Info" || events[0].DurationNS != int64(4200*time.Microsecond) {
		t.Errorf("unexpected first startup event: %#v", events[0])
	}
	if events[1].Operation != "Find" || events[1].DurationNS != int64(time.Second) {
		t.Errorf("unexpected second startup event: %#v", events[1])
	}
}

func validProbeReport(mode, supervisorSHA string) map[string]any {
	return map[string]any{
		"schema_version":      1,
		"launch_mode":         mode,
		"supervisor_sha256":   supervisorSHA,
		"goos":                runtime.GOOS,
		"goarch":              runtime.GOARCH,
		"go_version":          "go1.26.0",
		"revision":            "tested-revision",
		"binary_sha256":       "runtime-hash",
		"cold_cache_measured": false,
		"info":                validMeasurement("Info"),
		"find":                validMeasurement("Find"),
	}
}

func validMeasurement(operation string) map[string]any {
	warm := make([]any, 100)
	for index := range warm {
		warm[index] = map[string]any{"reaped": true}
	}
	return map[string]any{
		"operation":    operation,
		"first_launch": map[string]any{"reaped": true},
		"warm_samples": warm,
	}
}

func probeSection(report map[string]any, name string) map[string]any {
	return report[name].(map[string]any)
}
