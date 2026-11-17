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
	const startupPrefix = `{"ts":"2026-11-17T20:11:24.373Z","level":"debug",` +
		`"msg":"External runtime startup completed",`

	output := strings.Join([]string{
		startupPrefix + `"duration_ms":80,"operation":"Info"}`,
		`{"ts":"2026-11-17T20:11:24.791Z","level":"debug","msg":"External runtime startup completed",` +
			`"duration_ms":0,"operation":"Find","operation_id":"op-3","parent_operation_id":""}`,
		`{"ts":"2026-11-17T20:11:24.373+0100","level":"debug","msg":"External runtime startup completed",` +
			`"duration_ms":12,"operation":"Exec"}`,
		`{"ts":"2026-11-17T20:11:24.373+01:00","level":"debug","msg":"External runtime startup completed",` +
			`"duration_ms":13,"operation":"Logs"}`,
		`{"ts":"2026-11-17T20:11:24.373Z","level":"debug","msg":"External runtime startup completed","operation":"Info"}`,
		startupPrefix + `"duration_ms":null,"operation":"Info"}`,
		startupPrefix + `"duration_ms":-1,"operation":"Info"}`,
		startupPrefix + `"duration_ms":9223372036855,"operation":"Info"}`,
		startupPrefix + `"duration_ms":1.5,"operation":"Info"}`,
		startupPrefix + `"duration_ms":"80","operation":"Info"}`,
		`{"ts":"invalid","level":"debug","msg":"External runtime startup completed","duration_ms":80,"operation":"Info"}`,
		`{"ts":"2026-11-17T20:11:24.373Z","level":"info",` +
			`"msg":"External runtime startup completed","duration_ms":80,"operation":"Info"}`,
		`{"ts":"2026-11-17T20:11:24.373Z","level":"debug","msg":"different event","duration_ms":80,"operation":"Info"}`,
		startupPrefix + `"duration_ms":80,"operation":"Unknown"}`,
		`{"ts":"2026-11-17T20:11:24.373Z","level":"debug","msg":"External runtime startup completed","duration_ms":80}`,
		startupPrefix + `"duration_ms":80,"operation":"Info"`,
		"prefix " + startupPrefix + `"duration_ms":80,"operation":"Info"}`,
		startupPrefix + `"duration_ms":80,"operation":"Info"} {}`,
		`{"msg":"{\"msg\":\"External runtime startup completed\",\"operation\":\"Info\",\"duration_ms\":80}"}`,
		"DEBU External runtime Info startup completed in 4.2ms",
	}, "\n")

	events := parseStartupEvents(output)
	expected := []startupPerformance{
		{Operation: "Info", DurationNS: int64(80 * time.Millisecond)},
		{Operation: "Find", DurationNS: 0},
		{Operation: "Exec", DurationNS: int64(12 * time.Millisecond)},
		{Operation: "Logs", DurationNS: int64(13 * time.Millisecond)},
	}
	if len(events) != len(expected) {
		t.Fatalf("expected %#v, got %#v", expected, events)
	}
	for i := range expected {
		if events[i] != expected[i] {
			t.Errorf("event %d: expected %#v, got %#v", i, expected[i], events[i])
		}
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
