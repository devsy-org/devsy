package runargs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

const (
	imageReadMessage = "the runtime would read it as the image"
	labelFlag        = "--label"
	labelFileFlag    = "--label-file"
	positionalImage  = "busybox"
	reservedUser     = "devsy.user=secret-value"
	teamLabel        = "team=a"
	interactiveTTY   = "-it"
)

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

func (s *RunArgsSuite) TestAcceptsCommonRunArgs() {
	s.writeFile("labels.env", "team=a\n# devsy.user=commented\n\n")
	args := []string{
		"--cap-add=SYS_PTRACE",
		"--security-opt", "seccomp=unconfined",
		"--network", "host",
		interactiveTTY,
		"-v", "/a:/b",
		"-p8080:8080",
		"-eFOO=bar",
		labelFlag, teamLabel,
		"-l=team=b",
		"--entrypoint", "-x",
		labelFileFlag, "labels.env",
		"--future-flag=value",
		"--rm=true",
	}
	s.NoError(Validate(DockerCompatible, args, s.dir))
}

func (s *RunArgsSuite) TestRejectsTokensTheRuntimeReadsAsTheImage() {
	for _, args := range [][]string{
		{"--privileged", positionalImage},
		{"--tty", "false"},
		{"--network", "host", positionalImage, "sh"},
		{"--"},
		{"-"},
		{""},
		{"-li", teamLabel},
	} {
		s.ErrorContains(Validate(DockerCompatible, args, s.dir), imageReadMessage, args)
	}
}

func (s *RunArgsSuite) TestRejectsOptionsItCannotPlace() {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{labelFlag}, "runArgs: item 1: --label needs a value"},
		{[]string{interactiveTTY, "-e"}, "runArgs: item 2: -e needs a value"},
		{
			[]string{"--future-flag", "x"},
			"runArgs: item 1: unknown option --future-flag; write it as --future-flag=VALUE",
		},
		{[]string{"-itZ"}, "runArgs: item 1: unknown option -Z"},
		{
			[]string{"--rootfs"},
			"runArgs: item 1: option --rootfs makes the runtime read the image as a root filesystem",
		},
		{
			[]string{interactiveTTY, "--rootfs=true"},
			"runArgs: item 2: option --rootfs makes the runtime read the image as a root filesystem",
		},
	} {
		s.EqualError(Validate(DockerCompatible, tc.args, s.dir), tc.want, tc.args)
	}
}

func (s *RunArgsSuite) TestRejectsReservedLabelsInEveryForm() {
	s.writeFile("bom.env", "\uFEFFsh.devsy.recovery=secret-value\n")
	s.writeFile("spaced.env", "team=a\n  \tdevsy.user=secret-value\n")
	s.writeFile("key-only.env", "devsy.overlay.structure\n")
	for _, args := range [][]string{
		{labelFlag, reservedUser},
		{"--label=devsy.user=secret-value"},
		{"-l", reservedUser},
		{"-l=devsy.user=secret-value"},
		{"-ldevsy.user=secret-value"},
		{"-il", reservedUser},
		{labelFileFlag, "bom.env"},
		{"--label-file=spaced.env"},
		{labelFileFlag, "key-only.env"},
		{labelFileFlag, filepath.Join(s.dir, "bom.env")},
	} {
		err := Validate(DockerCompatible, args, s.dir)
		s.Require().Error(err, args)
		s.Contains(err.Error(), "is reserved by Devsy", args)
		s.NotContains(err.Error(), "secret-value", args)
	}
}

func (s *RunArgsSuite) TestRejectsLabelFilesItCannotReadAsTheRuntimeDoes() {
	s.EqualError(
		Validate(DockerCompatible, []string{labelFileFlag, "a.env,b.env"}, s.dir),
		`runArgs: label file path "a.env,b.env" must not contain a comma, quote or line break`,
	)
	s.EqualError(
		Validate(DockerCompatible, []string{labelFileFlag, `\labels.env`}, s.dir),
		`runArgs: label file path "\\labels.env" must be absolute or relative to the workspace`,
	)
	s.EqualError(
		Validate(DockerCompatible, []string{"--label-file=link/../labels.env"}, s.dir),
		`runArgs: label file path "link/../labels.env" must not contain a .. element`,
	)
	s.Require().NoError(os.Mkdir(filepath.Join(s.dir, "labels.d"), 0o700))
	s.EqualError(
		Validate(DockerCompatible, []string{labelFileFlag, "labels.d"}, s.dir),
		`runArgs: label file "labels.d" is not a regular file`,
	)
	s.ErrorContains(
		Validate(DockerCompatible, []string{labelFileFlag, "missing.env"}, s.dir),
		"runArgs: read label file:",
	)
}

func (s *RunArgsSuite) TestAppleRules() {
	s.NoError(
		Validate(AppleContainer, []string{"-il", teamLabel, "--cpus", "2", "-l=team=b"}, s.dir),
	)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-lteam=a"}, "runArgs: item 1: put the value of -l in the next item"},
		{[]string{"-li", teamLabel}, "runArgs: item 1: put the value of -l in the next item"},
		{
			[]string{labelFileFlag, "labels.env"},
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
	s.ErrorContains(
		Validate(AppleContainer, []string{"--rm", positionalImage}, s.dir),
		imageReadMessage,
	)
}

func (s *RunArgsSuite) TestOptionTablesNameEachOptionOnce() {
	for _, table := range []map[optionKind]string{dockerCompatibleOptions, appleContainerOptions} {
		seen := map[string]bool{}
		for _, list := range table {
			for name := range strings.FieldsSeq(list) {
				s.False(seen[name], name)
				seen[name] = true
				s.Regexp(`^(-[a-zA-Z]|--[a-z0-9][a-z0-9-]*)$`, name)
			}
		}
	}
}

func (s *RunArgsSuite) writeFile(name, content string) {
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, name), []byte(content), 0o600))
}
