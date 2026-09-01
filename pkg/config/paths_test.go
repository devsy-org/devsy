package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const (
	resultCurrent = "current"
	resultStale   = "stale"
)

type resultCommandTest struct {
	name                                            string
	primaryContent, fallbackContent                 string
	primarySelector, fallbackSelector               bool
	primarySelectorContent, fallbackSelectorContent string
	primaryTime, fallbackTime                       time.Time
	want                                            string
}

func writeResultTestFile(t *testing.T, path, content string) {
	t.Helper()
	// #nosec G306 -- test files intentionally use result-file permissions.
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type resultSelectorTest struct {
	path, resultPath string
	enabled          bool
	mtime            time.Time
}

func writeResultTestSelector(t *testing.T, test resultSelectorTest) {
	t.Helper()
	if !test.enabled {
		return
	}
	// #nosec G306 -- test files intentionally use selector permissions.
	if err := os.WriteFile(test.path, []byte(test.resultPath), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(test.path, test.mtime, test.mtime); err != nil {
		t.Fatal(err)
	}
}

func runResultCommandTest(t *testing.T, test resultCommandTest) ([]byte, error) {
	t.Helper()
	dir := t.TempDir()
	primary := filepath.Join(dir, "primary.json")
	fallback := filepath.Join(dir, "fallback.json")
	primaryPath := filepath.Join(dir, "primary.path")
	fallbackPath := filepath.Join(dir, "fallback.path")
	if test.primaryContent != "" {
		writeResultTestFile(t, primary, test.primaryContent)
	}
	if test.fallbackContent != "" {
		writeResultTestFile(t, fallback, test.fallbackContent)
	}
	primarySelectorContent := test.primarySelectorContent
	if primarySelectorContent == "" {
		primarySelectorContent = primary
	}
	fallbackSelectorContent := test.fallbackSelectorContent
	if fallbackSelectorContent == "" {
		fallbackSelectorContent = fallback
	}
	writeResultTestSelector(t, resultSelectorTest{
		path:       primaryPath,
		resultPath: primarySelectorContent,
		enabled:    test.primarySelector,
		mtime:      test.primaryTime,
	})
	writeResultTestSelector(t, resultSelectorTest{
		path:       fallbackPath,
		resultPath: fallbackSelectorContent,
		enabled:    test.fallbackSelector,
		mtime:      test.fallbackTime,
	})
	command := readDevContainerResultCommand(primary, fallback, primaryPath, fallbackPath)
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("skipping result command test: sh unavailable: %v", err)
	}
	// #nosec G204 -- command is generated from test-owned temporary paths.
	return exec.Command("sh", "-c", command).CombinedOutput()
}

func TestReadDevContainerResultCommandSelectsActiveSelector(t *testing.T) {
	tests := []resultCommandTest{
		{
			name:             "fallback",
			primaryContent:   resultStale,
			fallbackContent:  resultCurrent,
			fallbackSelector: true,
			primaryTime:      time.Unix(2, 0),
			fallbackTime:     time.Unix(2, 0),
			want:             resultCurrent,
		},
		{
			name:             "equal selector timestamps prefer primary",
			primaryContent:   resultCurrent,
			fallbackContent:  resultStale,
			primarySelector:  true,
			fallbackSelector: true,
			primaryTime:      time.Unix(2, 0),
			fallbackTime:     time.Unix(2, 0),
			want:             resultCurrent,
		},
		{
			name:            "primary",
			primaryContent:  resultCurrent,
			fallbackContent: resultStale,
			primarySelector: true,
			primaryTime:     time.Unix(2, 0),
			fallbackTime:    time.Unix(2, 0),
			want:            resultCurrent,
		},
		{
			name:             "missing primary",
			fallbackContent:  resultCurrent,
			primarySelector:  true,
			fallbackSelector: true,
			primaryTime:      time.Unix(2, 0),
			fallbackTime:     time.Unix(2, 0),
			want:             resultCurrent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, err := runResultCommandTest(t, test)
			if err != nil {
				t.Fatal(err)
			}
			if string(output) != test.want {
				t.Fatalf("result = %q, want %q", output, test.want)
			}
		})
	}
}

func TestReadDevContainerResultCommandRequiresSelector(t *testing.T) {
	_, err := runResultCommandTest(t, resultCommandTest{
		fallbackContent: resultStale,
	})
	if err == nil {
		t.Fatal("expected missing-selector error")
	}
}
