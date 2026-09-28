package pro

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	"github.com/stretchr/testify/suite"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const startTestHost = "pro.example.com"

type startSuite struct {
	suite.Suite
}

func TestStartSuite(t *testing.T) {
	suite.Run(t, new(startSuite))
}

// hasArg reports whether args contains the exact value.
func hasArg(args []string, want string) bool {
	return slices.Contains(args, want)
}

// hasFlagValue reports whether args contains want immediately after flag.
func hasFlagValue(args []string, flag, want string) bool {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) && args[i+1] == want {
			return true
		}
	}
	return false
}

// hasSet reports whether args contains "--set" followed by want.
func hasSet(args []string, want string) bool {
	return hasFlagValue(args, "--set", want)
}

func (s *startSuite) TestAppendHostArgsNoHostNoTunnel() {
	cmd := &StartCmd{}
	s.Empty(cmd.appendHostArgs(nil))
}

func (s *startSuite) TestAppendHostArgsNoTunnelDisablesRouter() {
	cmd := &StartCmd{NoTunnel: true}
	args := cmd.appendHostArgs(nil)
	// The router flag is passed with --set-string, not --set.
	s.True(
		hasFlagValue(args, "--set-string", "env.DISABLE_DEVSY_ROUTER=true"),
		"args: %v", args,
	)
	s.False(hasSet(args, "ingress.enabled=true"), "args: %v", args)
}

// A host enables ingress, pins the apex domain, and routes subdomains.
func (s *startSuite) TestAppendHostArgsConfiguresIngress() {
	cmd := &StartCmd{Host: startTestHost}
	args := cmd.appendHostArgs(nil)

	s.True(
		hasFlagValue(args, "--set-string", "env.DISABLE_DEVSY_ROUTER=true"),
		"args: %v", args,
	)
	for _, want := range []string{
		"ingress.enabled=true",
		"ingress.host=pro.example.com",
		"env.DEVSY_HOST=pro.example.com",
		"devsyIngress.enabled=true",
		"devsyIngress.host=*.pro.example.com",
		"env.DEVSY_SUBDOMAIN=*.pro.example.com",
	} {
		s.True(hasSet(args, want), "missing %q in %v", want, args)
	}
}

func (s *startSuite) TestAppendHostArgsPreservesExistingArgs() {
	cmd := &StartCmd{Host: startTestHost}
	args := cmd.appendHostArgs([]string{"--first"})
	s.Equal("--first", args[0])
}

func (s *startSuite) TestAppendReleaseArgsEmpty() {
	cmd := &StartCmd{}
	s.Empty(cmd.appendReleaseArgs(nil))
}

func (s *startSuite) TestAppendReleaseArgsVersionAndProduct() {
	cmd := &StartCmd{Version: "v1.2.3", Product: "loft"}
	args := cmd.appendReleaseArgs(nil)
	s.True(hasArg(args, "--version"), "args: %v", args)
	s.True(hasArg(args, "v1.2.3"), "args: %v", args)
	s.True(hasSet(args, "product=loft"), "args: %v", args)
}

// Reuse-values is only meaningful when not resetting, since a reset discards
// the previous release values.
func (s *startSuite) TestAppendReleaseArgsReuseValuesRespectsReset() {
	reusing := &StartCmd{ReuseValues: true}
	s.True(hasArg(reusing.appendReleaseArgs(nil), "--reuse-values"))

	resetting := &StartCmd{ReuseValues: true, Reset: true}
	s.False(hasArg(resetting.appendReleaseArgs(nil), "--reuse-values"))
}

func (s *startSuite) TestAppendReleaseArgsReuseValuesNotRequested() {
	cmd := &StartCmd{}
	s.False(hasArg(cmd.appendReleaseArgs(nil), "--reuse-values"))
}

func (s *startSuite) TestWritePasswordValuesFileContents() {
	name, err := writePasswordValuesFile("s3cret")
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = os.Remove(name) })

	data, err := os.ReadFile(name) //nolint:gosec // test reads a temp file it just created
	s.Require().NoError(err)
	s.Equal("admin:\n  password: \"s3cret\"\n", string(data))
}

func (s *startSuite) TestWritePasswordValuesFileQuotesAwkwardPasswords() {
	name, err := writePasswordValuesFile(`a"b`)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = os.Remove(name) })

	data, err := os.ReadFile(name) //nolint:gosec // test reads a temp file it just created
	s.Require().NoError(err)
	// %q escaping keeps the value a single valid YAML scalar.
	s.Contains(string(data), `password: "a\"b"`)
}

func (s *startSuite) TestBuildUpgradeArgsAddsPasswordValuesFile() {
	cmd := &StartCmd{Password: "s3cret"}
	args, cleanup, err := cmd.buildUpgradeArgs()
	s.Require().NoError(err)
	s.T().Cleanup(cleanup)

	index := -1
	for i, arg := range args {
		if arg == "--values" {
			index = i
			break
		}
	}
	s.Require().NotEqual(-1, index, "args: %v", args)
	s.Require().Less(index+1, len(args), "args: %v", args)
	file := args[index+1]
	s.FileExists(file)

	data, err := os.ReadFile(file) //nolint:gosec // temp file the command created
	s.Require().NoError(err)
	s.Contains(string(data), "s3cret")

	// The generated values file must not outlive the command.
	cleanup()
	s.NoFileExists(file)
}

func (s *startSuite) TestBuildUpgradeArgsNoPasswordMeansNoValuesFile() {
	cmd := &StartCmd{}
	args, cleanup, err := cmd.buildUpgradeArgs()
	s.Require().NoError(err)
	s.T().Cleanup(cleanup)
	s.False(hasArg(args, "--values"), "args: %v", args)
}

// A caller-supplied values file is passed as an absolute path, since helm runs
// with its own working directory.
func (s *startSuite) TestBuildUpgradeArgsAbsolutizesValuesPath() {
	dir := s.T().TempDir()
	relative := filepath.Join(dir, "values.yaml")
	s.Require().NoError(os.WriteFile(relative, []byte("a: b\n"), 0o600))

	cmd := &StartCmd{Values: relative}
	args, cleanup, err := cmd.buildUpgradeArgs()
	s.Require().NoError(err)
	s.T().Cleanup(cleanup)
	s.True(hasArg(args, relative), "args: %v", args)
	if !filepath.IsAbs(relative) {
		s.Fail("test expects an absolute input path")
	}
}

func (s *startSuite) TestBuildUpgradeArgsCombinesHostReleaseAndValues() {
	cmd := &StartCmd{Host: startTestHost, Version: "v1.2.3", Product: "loft"}
	args, cleanup, err := cmd.buildUpgradeArgs()
	s.Require().NoError(err)
	s.T().Cleanup(cleanup)

	s.True(hasSet(args, "ingress.host="+startTestHost), "args: %v", args)
	s.True(hasArg(args, "v1.2.3"), "args: %v", args)
	s.True(hasSet(args, "product=loft"), "args: %v", args)
}

func (s *startSuite) TestPasswordRefIncomplete() {
	for _, tc := range []struct {
		name string
		ref  *storagev1.SecretRef
		want bool
	}{
		{"nil", nil, true},
		{"empty name", &storagev1.SecretRef{SecretNamespace: "ns"}, true},
		{"empty namespace", &storagev1.SecretRef{SecretName: "name"}, true},
		{"complete", &storagev1.SecretRef{SecretName: "name", SecretNamespace: "ns"}, false},
	} {
		s.Run(tc.name, func() {
			s.Equal(tc.want, passwordRefIncomplete(tc.ref))
		})
	}
}

func (s *startSuite) TestDeleteIgnoreNotFoundRunsEveryDelete() {
	var ran []string
	record := func(name string) func() error {
		return func() error {
			ran = append(ran, name)
			return nil
		}
	}

	s.Require().NoError(deleteIgnoreNotFound(record("a"), record("b")))
	s.Equal([]string{"a", "b"}, ran)
}

func (s *startSuite) TestDeleteIgnoreNotFoundSkipsNotFound() {
	ran := false
	notFound := kerrors.NewNotFound(
		schema.GroupResource{Resource: "secrets"},
		"missing",
	)

	err := deleteIgnoreNotFound(
		func() error { ran = true; return notFound },
		func() error { ran = true; return nil },
	)
	s.Require().NoError(err)
	s.True(ran, "subsequent deletes must still run")
}

func (s *startSuite) TestDeleteIgnoreNotFoundPropagatesOtherErrors() {
	sentinel := os.ErrPermission
	ran := false

	err := deleteIgnoreNotFound(
		func() error { return sentinel },
		func() error { ran = true; return nil },
	)
	s.ErrorIs(err, sentinel)
	s.False(ran, "must stop at the first real error")
}

func (s *startSuite) TestDeleteIgnoreNotFoundWithNoDeletes() {
	s.NoError(deleteIgnoreNotFound())
}

// Guard the shape of the generated values document, since helm consumes it.
func (s *startSuite) TestWritePasswordValuesFileIsValidYAMLShape() {
	name, err := writePasswordValuesFile("s3cret")
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = os.Remove(name) })

	data, err := os.ReadFile(name) //nolint:gosec // test reads a temp file it just created
	s.Require().NoError(err)

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	s.Require().Len(lines, 2, "content: %q", string(data))
	s.Equal("admin:", lines[0])
	s.Equal(`  password: "s3cret"`, lines[1])
}
