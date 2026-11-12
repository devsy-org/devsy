package runargs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

const imageReadMessage = "the runtime would read it as the image"

type RunArgsSuite struct {
	suite.Suite
	dir string
}

func TestRunArgsSuite(t *testing.T) {
	suite.Run(t, new(RunArgsSuite))
}

func (s *RunArgsSuite) SetupTest() {
	s.dir = s.T().TempDir()
}

func (s *RunArgsSuite) writeFile(name, content string) {
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, name), []byte(content), 0o600))
}

func (s *RunArgsSuite) TestAcceptsCommonRunArgs() {
	s.writeFile("labels.env", "team=a\n# devsy.user=commented\n\n")
	args := []string{
		"--cap-add=SYS_PTRACE",
		"--security-opt", "seccomp=unconfined",
		"--network", "host",
		"-it",
		"-v", "/a:/b",
		"-p8080:8080",
		"-eFOO=bar",
		"--label", "team=a",
		"-l=team=b",
		"--entrypoint", "-x",
		"--label-file", "labels.env",
		"--future-flag=value",
		"--rm=true",
	}
	s.NoError(Validate(DockerCompatible, args, s.dir))
}

func (s *RunArgsSuite) TestRejectsTokensTheRuntimeReadsAsTheImage() {
	for _, args := range [][]string{
		{"--privileged", "busybox"},
		{"--tty", "false"},
		{"--network", "host", "busybox", "sh"},
		{"--"},
		{"-"},
		{""},
		{"-li", "team=a"},
	} {
		s.ErrorContains(Validate(DockerCompatible, args, s.dir), imageReadMessage, args)
	}
}

func (s *RunArgsSuite) TestRejectsOptionsItCannotPlace() {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--label"}, "runArgs: item 1: --label needs a value"},
		{[]string{"-it", "-e"}, "runArgs: item 2: -e needs a value"},
		{
			[]string{"--future-flag", "x"},
			"runArgs: item 1: unknown option --future-flag; write it as --future-flag=VALUE",
		},
		{[]string{"-itZ"}, "runArgs: item 1: unknown option -Z"},
	} {
		s.EqualError(Validate(DockerCompatible, tc.args, s.dir), tc.want, tc.args)
	}
}

func (s *RunArgsSuite) TestRejectsReservedLabelsInEveryForm() {
	s.writeFile("bom.env", "\uFEFFsh.devsy.recovery=secret-value\n")
	s.writeFile("spaced.env", "team=a\n  \tdevsy.user=secret-value\n")
	s.writeFile("key-only.env", "devsy.overlay.structure\n")
	for _, args := range [][]string{
		{"--label", "devsy.user=secret-value"},
		{"--label=devsy.user=secret-value"},
		{"-l", "devsy.user=secret-value"},
		{"-l=devsy.user=secret-value"},
		{"-ldevsy.user=secret-value"},
		{"-il", "devsy.user=secret-value"},
		{"--label-file", "bom.env"},
		{"--label-file=spaced.env"},
		{"--label-file", "key-only.env"},
		{"--label-file", filepath.Join(s.dir, "bom.env")},
	} {
		err := Validate(DockerCompatible, args, s.dir)
		s.Require().Error(err, args)
		s.Contains(err.Error(), "is reserved by Devsy", args)
		s.NotContains(err.Error(), "secret-value", args)
	}
}

func (s *RunArgsSuite) TestRejectsLabelFilesItCannotReadAsTheRuntimeDoes() {
	s.EqualError(
		Validate(DockerCompatible, []string{"--label-file", "a.env,b.env"}, s.dir),
		`runArgs: label file path "a.env,b.env" must not contain a comma, quote or line break`,
	)
	s.EqualError(
		Validate(DockerCompatible, []string{"--label-file", `\labels.env`}, s.dir),
		`runArgs: label file path "\\labels.env" must be absolute or relative to the workspace`,
	)
	s.ErrorContains(
		Validate(DockerCompatible, []string{"--label-file", "missing.env"}, s.dir),
		"runArgs: read label file:",
	)
}

func (s *RunArgsSuite) TestAppleRules() {
	s.NoError(
		Validate(AppleContainer, []string{"-il", "team=a", "--cpus", "2", "-l=team=b"}, s.dir),
	)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-lteam=a"}, "runArgs: item 1: put the value of -l in the next item"},
		{[]string{"-li", "team=a"}, "runArgs: item 1: put the value of -l in the next item"},
		{
			[]string{"--label-file", "labels.env"},
			"runArgs: item 1: unknown option --label-file; write it as --label-file=VALUE",
		},
		{[]string{"-h"}, "runArgs: item 1: unknown option -h"},
		{
			[]string{"-l", "devsy.user=root"},
			`runArgs: label "devsy.user" is reserved by Devsy; remove the user-supplied label`,
		},
	} {
		s.EqualError(Validate(AppleContainer, tc.args, s.dir), tc.want, tc.args)
	}
	s.ErrorContains(Validate(AppleContainer, []string{"--rm", "busybox"}, s.dir), imageReadMessage)
}

func (s *RunArgsSuite) TestOptionTablesNameEachOptionOnce() {
	for _, table := range []map[optionKind]string{dockerCompatibleOptions, appleContainerOptions} {
		seen := map[string]bool{}
		for _, list := range table {
			for _, name := range strings.Fields(list) {
				s.False(seen[name], name)
				seen[name] = true
				s.Regexp(`^(-[a-zA-Z]|--[a-z0-9][a-z0-9-]*)$`, name)
			}
		}
	}
}
