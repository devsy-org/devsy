package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSentinelCanaryRequired(t *testing.T) {
	record, err := json.Marshal(sentinelCanary)
	if err != nil {
		t.Fatal(err)
	}
	err = scanSentinels(strings.NewReader(""), nil)
	if err == nil ||
		err.Error() != "no canary record: sentinel file was not populated by the e2e process" {
		t.Fatalf("unexpected missing canary result: %v", err)
	}
	if err := scanSentinels(
		strings.NewReader(string(record)+"\n"),
		[]byte("public\n"),
	); err != nil {
		t.Fatal(err)
	}
	if err := scanSentinels(
		strings.NewReader(string(record)+"\n"),
		[]byte(sentinelCanary),
	); err == nil {
		t.Fatal("canary leak accepted")
	}
}

func TestScanSentinels(t *testing.T) {
	input := "\"missing\"\n\"multi\\nline\"\n\"multi\\nline\"\n" +
		strconv.Quote(sentinelCanary) + "\n"
	err := scanSentinels(strings.NewReader(input), []byte("public\nmulti\nline\n"))
	if err == nil || err.Error() != "sentinel 2 found at line 2" {
		t.Fatalf("unexpected result: %v", err)
	}
	if err := scanSentinels(strings.NewReader(input), []byte("public\n")); err != nil {
		t.Fatal(err)
	}
	if err := scanSentinels(strings.NewReader("invalid\n"), nil); err == nil {
		t.Fatal("invalid sentinel accepted")
	}
}

func TestFrameworkCanaryMatches(t *testing.T) {
	files, err := filepath.Glob("../../e2e/framework/*.go")
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, name := range files {
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if value := frameworkCanaryLiteral(node); value != "" {
				found = value
			}
			return true
		})
	}
	if found != sentinelCanary {
		t.Fatal("framework and checker canary constants differ")
	}
}

func frameworkCanaryLiteral(node ast.Node) string {
	spec, ok := node.(*ast.ValueSpec)
	if !ok || len(spec.Names) != 1 || spec.Names[0].Name != "sentinelCanary" ||
		len(spec.Values) != 1 {
		return ""
	}
	value, ok := spec.Values[0].(*ast.BasicLit)
	if !ok {
		return ""
	}
	decoded, _ := strconv.Unquote(value.Value)
	return decoded
}

func TestSentinelPathForwardedToSuite(t *testing.T) {
	for _, tc := range []struct {
		goos, path string
	}{
		{osLinux, "/tmp/e2e-sentinels.jsonl"},
		{osWindows, `C:\runner temp\e2e-sentinels.jsonl`},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			runner := e2eRunner{
				getenv: func(key string) string {
					if key == "DEVSY_E2E_SENTINEL_FILE" {
						return tc.path
					}
					return ""
				},
				opts: e2eOptions{GOOS: tc.goos},
			}
			spec := runner.binarySpec(Suite{Resolved: true})
			env := spec.Env
			if tc.goos == osLinux {
				env = spec.Args
			}
			value, ok := e2eEnvValue(env, "DEVSY_E2E_SENTINEL_FILE")
			if !ok || value != tc.path {
				t.Fatalf("sentinel path = %q, present = %t", value, ok)
			}
		})
	}
}
