package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type microsandboxPinsSuite struct {
	suite.Suite
}

func TestMicrosandboxPins(t *testing.T) {
	suite.Run(t, new(microsandboxPinsSuite))
}

func (s *microsandboxPinsSuite) TestExternalRuntimeMinimumMatchesCIAndDocs() {
	minimum := strings.TrimPrefix(microsandboxVersion, "v")
	s.Regexp(
		`microsandboxExternalMinimumVersion\s*=\s*"`+regexp.QuoteMeta(minimum)+`"`,
		s.read("e2e/tests/up/microsandbox_session.go"),
	)
	s.Contains(
		s.read("sites/docs-devsy-sh/content/docs/managing-providers/microsandbox.mdx"),
		"| External provider | "+minimum+" |",
	)
	s.Contains(s.read("AGENTS.md"), "The Linux CI bundle uses msb "+minimum+" and")
	workflow := s.read(".github/workflows/pr-ci.yml")
	s.Contains(workflow, "path: ${{ runner.temp }}/"+microsandboxArchive+"\n")
	s.Contains(
		workflow,
		"key: ${{ runner.os }}-${{ runner.arch }}-microsandbox-bundle-"+microsandboxVersion+"\n",
	)
}

func (s *microsandboxPinsSuite) read(path string) string {
	s.T().Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(path)))
	s.Require().NoError(err)
	return strings.ReplaceAll(string(content), "\r\n", "\n")
}
