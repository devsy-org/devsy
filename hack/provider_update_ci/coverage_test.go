package main

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCoverageIncludesDiscoveredRootsAndEverySubtest(t *testing.T) {
	root := fixtureTests[0]
	child := testID{packagePath: root.packagePath, name: root.name + "/new_case"}
	expected := map[testID]struct{}{root: {}}
	events := map[testID]testState{
		root:  {started: true, passed: true},
		child: {started: true, skipped: true},
	}
	require.NotEmpty(
		t,
		validateTestCoverage(expected, events),
		"a new skipped subtest must fail without a registry edit",
	)
	events[child] = testState{started: true, passed: true}
	require.Empty(t, validateTestCoverage(expected, events))
}

func TestCoverageFailsClosed(t *testing.T) {
	id := fixtureTests[0]
	for name, state := range map[string]testState{
		"missing run": {passed: true},
		"unfinished":  {started: true},
		"skipped":     {started: true, skipped: true},
		"failed":      {started: true, failed: true},
	} {
		t.Run(name, func(t *testing.T) {
			require.NotEmpty(
				t,
				validateTestCoverage(map[testID]struct{}{id: {}}, map[testID]testState{id: state}),
			)
		})
	}
	require.NotEmpty(t, validateTestCoverage(nil, nil))
	require.NotEmpty(t, validateTestCoverage(map[testID]struct{}{id: {}}, nil))
}

func TestCoverageCannotSubstituteAnotherPackage(t *testing.T) {
	id := fixtureTests[0]
	other := testID{packagePath: providerUpdatePackages[1], name: id.name}
	require.NotEmpty(t, validateTestCoverage(
		map[testID]struct{}{id: {}},
		map[testID]testState{other: {started: true, passed: true}},
	))
}

func TestCaptureDiscoversRootsAndSeparatesPackages(t *testing.T) {
	capture := newOutputCapture(io.Discard, io.Discard, io.Discard)
	capture.readEvent([]byte(`{"Action":"output","Package":"one","Output":"TestNewCase\n"}`))
	capture.readEvent([]byte(`{"Action":"output","Package":"two","Output":"TestNewCase\n"}`))
	capture.readEvent(
		[]byte(`{"Action":"output","Package":"one","Test":"TestParent","Output":"TestBogus\n"}`),
	)
	require.Len(t, capture.listedTests(), 2)
	for _, name := range []string{"TestNewCase/sub", "Test new", "ok package", ""} {
		require.False(t, isListedTestName(name))
	}
}

func TestCLIDiscoveryFailsClosed(t *testing.T) {
	for _, mode := range []string{"empty-discovery", "missing-package", "discovery-failure"} {
		t.Run(mode, func(t *testing.T) {
			setUpFakeGo(t, mode, "0")
			_, stderr, code := runCLIProcess(t, "--artifacts-dir", t.TempDir())
			require.NotZero(t, code)
			require.Contains(t, stderr, "discover")
		})
	}
}
