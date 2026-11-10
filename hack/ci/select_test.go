package main

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

const (
	selectionGoMod            = "go.mod"
	selectionMCPDir           = "pkg/mcp"
	selectionMCPFile          = "pkg/mcp/server.go"
	selectionNestedMCPFile    = "pkg/mcp/internal/server.go"
	selectionSegmentPattern   = "pkg/*/server.go"
	selectionAgentPattern     = "cmd/internal/agent*.go"
	selectionMCPPattern       = "pkg/mcp/**"
	selectionRecursivePattern = "pkg/**/server.go"
	selectionMultiplePattern  = "**/a/**/b.go"
	selectionCommandFile      = "cmd/mcp/serve.go"
	selectionGlobstar         = "**"
	selectionSmokeLabel       = "smoke"
	selectionLeafLabel        = "leaf"
	selectionSecretLabel      = "secret"
	selectionLeafFile         = "pkg/leaf/file.go"
	selectionCoreFile         = "pkg/core/file.go"
)

type selectionSuite struct {
	suite.Suite
}

func TestSelectionSuite(t *testing.T) {
	suite.Run(t, new(selectionSuite))
}

func (s *selectionSuite) TestGlobBoundaries() {
	tests := []struct {
		name    string
		pattern string
		file    string
		want    bool
	}{
		{"literal", selectionGoMod, selectionGoMod, true},
		{"literal suffix", selectionGoMod, "go.mod.bak", false},
		{"literal directory", selectionMCPDir, selectionMCPFile, false},
		{"segment", selectionSegmentPattern, selectionMCPFile, true},
		{"segment does not descend", selectionSegmentPattern, selectionNestedMCPFile, false},
		{"segment does not disappear", selectionSegmentPattern, "pkg/server.go", false},
		{"filename star empty", selectionAgentPattern, "cmd/internal/agent.go", true},
		{"filename star suffix", selectionAgentPattern, "cmd/internal/agent_daemon.go", true},
		{
			"filename star stops at slash",
			selectionAgentPattern,
			"cmd/internal/agent/daemon.go",
			false,
		},
		{"recursive immediate child", selectionMCPPattern, selectionMCPFile, true},
		{"recursive deep child", selectionMCPPattern, selectionNestedMCPFile, true},
		{"recursive empty suffix", selectionMCPPattern, selectionMCPDir, true},
		{"recursive sibling prefix", selectionMCPPattern, "pkg/mcp-other/server.go", false},
		{"globstar zero directories", selectionRecursivePattern, "pkg/server.go", true},
		{"globstar one directory", selectionRecursivePattern, selectionMCPFile, true},
		{"globstar many directories", selectionRecursivePattern, selectionNestedMCPFile, true},
		{"globstar requires suffix", selectionRecursivePattern, "pkg/mcp/client.go", false},
		{"globstar backtracks", "**/a/b.go", "a/x/a/b.go", true},
		{"multiple globstars", selectionMultiplePattern, "x/a/y/a/z/b.go", true},
		{"multiple empty globstars", selectionMultiplePattern, "a/b.go", true},
		{"multiple globstars missing suffix", selectionMultiplePattern, "x/a/y/c.go", false},
		{"root globstar", "**/*.go", "main.go", true},
		{"root globstar nested", "**/*.go", selectionCommandFile, true},
		{"single star root only", "*.go", selectionCommandFile, false},
		{"windows changed path", "cmd/mcp/**", `cmd\mcp\serve.go`, true},
		{"windows pattern", `cmd\mcp\**`, selectionCommandFile, true},
		{"relative path prefix", "./cmd/mcp/**", "./cmd/mcp/serve.go", true},
		{"duplicate slash", "cmd//mcp/**", "cmd/mcp//serve.go", true},
		{"character class", "pkg/[ab]/*.go", "pkg/a/file.go", true},
		{"character class boundary", "pkg/[ab]/*.go", "pkg/c/file.go", false},
		{"invalid pattern", "pkg/[", "pkg/file.go", false},
		{"empty changed path", selectionGlobstar, "", false},
		{"absolute changed path", selectionGlobstar, "/cmd/mcp/serve.go", false},
		{"parent changed path", selectionGlobstar, "../cmd/mcp/serve.go", false},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.Equal(tt.want, matchGlob(tt.pattern, tt.file), "%q against %q", tt.pattern, tt.file)
		})
	}
}

func (s *selectionSuite) TestSelectionPrecedenceAndSecrets() {
	m := &Manifest{
		Selection: SelectionConfig{
			FullRunPaths: []string{"hack/ci/**"},
			CorePaths:    []string{"pkg/core/**"},
		},
		E2E: E2ETier{Suites: []Suite{
			{
				Group:   groupIntegration,
				Label:   selectionSmokeLabel,
				PRSmoke: true,
				Paths:   []string{"pkg/smoke/**"},
			},
			{Group: groupIntegration, Label: selectionLeafLabel, Paths: []string{"pkg/leaf/**"}},
			{
				Group: groupIntegration, Label: selectionSecretLabel, PRSmoke: true,
				RequiresSecret: true, Paths: []string{"pkg/private/**"},
			},
			{
				Group:   groupMicrosandbox,
				Label:   "other",
				PRSmoke: true,
				Paths:   []string{"pkg/leaf/**"},
			},
		}},
	}
	for _, tt := range selectionPrecedenceCases {
		s.Run(tt.name, func() {
			selected := selectSuites(m, groupIntegration, selectionOptions{
				secretAvailable: tt.secret,
				mode:            tt.mode,
				changedFiles:    tt.files,
			})
			s.NotNil(selected)
			s.Equal(tt.want, selectionLabels(selected))
		})
	}
	s.Empty(
		selectSuites(
			m,
			"missing",
			selectionOptions{secretAvailable: true, mode: selectionModeFull},
		),
	)
	opts := selectionOptions{
		secretAvailable: true,
		mode:            selectionModePR,
		changedFiles:    []string{selectionLeafFile},
	}
	s.Equal([]string{"other"}, selectionLabels(selectSuites(m, groupMicrosandbox, opts)))
}

func (s *selectionSuite) TestEmbeddedLeafAndDocumentationSelection() {
	m, err := LoadManifest()
	s.Require().NoError(err)
	for _, group := range []string{groupUnprivileged, groupIntegration, groupMicrosandbox} {
		s.Run(group, func() {
			want := []string{}
			if group == groupIntegration {
				want = []string{"mcp"}
			}
			leaf := selectionOptions{
				secretAvailable: true,
				mode:            selectionModePR,
				changedFiles:    []string{`cmd\mcp\serve.go`},
			}
			docs := selectionOptions{
				secretAvailable: true, mode: selectionModePR,
				changedFiles: []string{"sites/docs-devsy-sh/content/docs/index.mdx"},
			}
			s.Equal(want, selectionLabels(selectSuites(m, group, leaf)))
			s.Equal([]string{}, selectionLabels(selectSuites(m, group, docs)))
		})
	}
}

func (s *selectionSuite) TestEmbeddedCoreSmokeTier() {
	m, err := LoadManifest()
	s.Require().NoError(err)
	var smoke []string
	opts := selectionOptions{
		secretAvailable: true,
		mode:            selectionModePR,
		changedFiles:    []string{"cmd/root.go"},
	}
	for _, group := range []string{groupUnprivileged, groupIntegration, groupMicrosandbox} {
		for _, selected := range selectSuites(m, group, opts) {
			smoke = append(smoke, selected.Runner+"/"+selected.Label)
		}
	}
	s.ElementsMatch([]string{
		"ubuntu-latest/context",
		"ubuntu-latest/extends",
		"ubuntu-latest/up-workspaces",
		"ubuntu-latest/up-provider-docker",
		"ubuntu-latest/up-docker-compose",
		"ubuntu-latest/up-features",
		"ubuntu-latest/ssh",
		"ubuntu-latest/build",
		"ubuntu-latest/up-provider-kubernetes",
		"ubuntu-24.04/up-provider-podman-rootless-basic",
		"ubuntu-24.04/up-provider-podman-rootful-basic",
		"windows-latest/up-docker-wsl",
		"ubuntu-latest/up-provider-microsandbox",
	}, smoke)
}

func (s *selectionSuite) TestInactivityImplementationSelectsLongRunningSuite() {
	m, err := LoadManifest()
	s.Require().NoError(err)
	for _, file := range []string{
		"cmd/internal/agentcontainer/daemon.go",
		"cmd/internal/agentcontainer/ssh_server.go",
		"pkg/daemon/agent/lifecycle_monitor.go",
	} {
		opts := selectionOptions{
			secretAvailable: true,
			mode:            selectionModePR,
			changedFiles:    []string{file},
		}
		selected := selectSuites(m, groupIntegration, opts)
		s.Contains(selectionLabels(selected), "up-provider-kubernetes-inactivity", file)
	}
}

func selectionLabels(suites []Suite) []string {
	labels := make([]string, 0, len(suites))
	for _, selected := range suites {
		labels = append(labels, selected.Label)
	}
	return labels
}

var selectionPrecedenceCases = []struct {
	name   string
	mode   string
	files  []string
	secret bool
	want   []string
}{
	{"default full", "", nil, false, []string{selectionSmokeLabel, selectionLeafLabel}},
	{
		"explicit full with secret", selectionModeFull, nil, true,
		[]string{selectionSmokeLabel, selectionLeafLabel, selectionSecretLabel},
	},
	{
		"full ignores unrelated files", selectionModeFull,
		[]string{"docs/readme.md"},
		false,
		[]string{selectionSmokeLabel, selectionLeafLabel},
	},
	{"pr empty", selectionModePR, nil, true, []string{}},
	{"pr unrelated", selectionModePR, []string{"docs/readme.md"}, true, []string{}},
	{
		"leaf excludes unrelated smoke",
		selectionModePR,
		[]string{selectionLeafFile},
		false,
		[]string{selectionLeafLabel},
	},
	{
		"core selects smoke",
		selectionModePR,
		[]string{selectionCoreFile},
		false,
		[]string{selectionSmokeLabel},
	},
	{
		"core includes secret when available", selectionModePR,
		[]string{selectionCoreFile},
		true,
		[]string{selectionSmokeLabel, selectionSecretLabel},
	},
	{
		"direct secret excluded",
		selectionModePR,
		[]string{"pkg/private/file.go"},
		false,
		[]string{},
	},
	{
		"direct secret included",
		selectionModePR,
		[]string{"pkg/private/file.go"},
		true,
		[]string{selectionSecretLabel},
	},
	{
		"core plus leaf union", selectionModePR,
		[]string{selectionLeafFile, selectionCoreFile},
		true,
		[]string{selectionSmokeLabel, selectionLeafLabel, selectionSecretLabel},
	},
	{
		"overlapping and repeated changes", selectionModePR,
		[]string{selectionCoreFile, "pkg/smoke/file.go", selectionCoreFile},
		false,
		[]string{selectionSmokeLabel},
	},
	{
		"full trigger excludes secret", selectionModePR,
		[]string{"hack/ci/select.go"},
		false,
		[]string{selectionSmokeLabel, selectionLeafLabel},
	},
	{
		"full trigger includes secret", selectionModePR,
		[]string{"hack/ci/select.go"},
		true,
		[]string{selectionSmokeLabel, selectionLeafLabel, selectionSecretLabel},
	},
}
