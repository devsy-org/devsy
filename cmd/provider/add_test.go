package provider

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	devsyhttp "github.com/devsy-org/devsy/pkg/http"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/suite"
)

const externalMicrosandboxManifest = `name: microsandbox
version: v0.1.8
agent:
  path: devsy
  driver: external
  external:
    binary: DEVSY_RUNTIME_DRIVER
  binaries:
    DEVSY_RUNTIME_DRIVER:
      - os: linux
        arch: amd64
        path: https://example.invalid/devsy-runtime
        checksum: "0000000000000000000000000000000000000000000000000000000000000000"
exec:
  command: echo hi
  create: echo hi
  delete: echo hi
`

type addDeprecationSuite struct {
	suite.Suite
}

func TestAddDeprecationSuite(t *testing.T) {
	suite.Run(t, new(addDeprecationSuite))
}

func (s *addDeprecationSuite) SetupTest() {
	client := devsyhttp.GetHTTPClient()
	previousTransport := client.Transport
	client.Transport = providerAddRoundTripper(func(req *http.Request) (*http.Response, error) {
		const manifestURL = "https://github.com/devsy-org/devsy-provider-microsandbox/" +
			"releases/latest/download/provider.yaml"
		if req.URL.String() != manifestURL {
			s.T().Fatalf("unexpected source request: %s", req.URL)
		}
		return providerAddResponse(req, externalMicrosandboxManifest), nil
	})
	s.T().Cleanup(func() { client.Transport = previousTransport })
}

func (s *addDeprecationSuite) TestMicrosandboxAliasUsesLatestExternalProvider() {
	cmd := &AddCmd{GlobalFlags: &flags.GlobalFlags{}}
	for _, tc := range []struct {
		name string
		want string
	}{
		{name: "custom-msb", want: "custom-msb"},
		{want: providerpkg.MicrosandboxDriver},
	} {
		resolved, err := cmd.resolveAddSource(
			context.Background(),
			[]string{providerpkg.MicrosandboxDriver},
			tc.name,
		)
		s.Require().NoError(err)
		s.Require().Equal(tc.want, resolved.name)
		s.Require().Equal("github.com/devsy-org/devsy-provider-microsandbox", resolved.source.Raw)
		s.Require().Equal("devsy-org/devsy-provider-microsandbox", resolved.source.Github)
		parsed, err := providerpkg.ParseProvider(bytes.NewReader(resolved.raw))
		s.Require().NoError(err)
		s.Require().Equal(providerpkg.ExternalDriver, parsed.Agent.Driver)
	}
}

func (s *addDeprecationSuite) TestExplicitMicrosandboxPinIsPreserved() {
	const pinnedSource = "github.com/devsy-org/devsy-provider-microsandbox@v0.1.8"
	client := devsyhttp.GetHTTPClient()
	client.Transport = providerAddRoundTripper(func(req *http.Request) (*http.Response, error) {
		s.Require().Equal("https://github.com/devsy-org/devsy-provider-microsandbox/"+
			"releases/download/v0.1.8/provider.yaml", req.URL.String())
		return providerAddResponse(req, externalMicrosandboxManifest), nil
	})
	cmd := &AddCmd{GlobalFlags: &flags.GlobalFlags{}}
	resolved, err := cmd.resolveAddSource(context.Background(), []string{pinnedSource}, "")
	s.Require().NoError(err)
	s.Require().Equal(providerpkg.MicrosandboxDriver, resolved.name)
	s.Require().Equal(pinnedSource, resolved.source.Raw)
	s.Require().Equal("devsy-org/devsy-provider-microsandbox", resolved.source.Github)
}

func (s *addDeprecationSuite) TestCloneAndOtherSourcesRemainUnchanged() {
	cmd := &AddCmd{GlobalFlags: &flags.GlobalFlags{}, FromExisting: "docker"}
	resolved, err := cmd.resolveAddSource(
		context.Background(),
		[]string{providerpkg.MicrosandboxDriver},
		"named-copy",
	)
	s.Require().NoError(err)
	s.Require().Equal("named-copy", resolved.name)
	s.Require().Nil(resolved.source)

	cmd.FromExisting = ""
	manifest := filepath.Join(s.T().TempDir(), "provider.yaml")
	err = os.WriteFile(manifest, []byte("name: parsed\nagent:\n  path: devsy\n"), 0o600)
	s.Require().NoError(err)
	resolved, err = cmd.resolveAddSource(context.Background(), []string{manifest}, "custom")
	s.Require().NoError(err)
	s.Require().Equal("custom", resolved.name)
	s.Require().Equal(manifest, resolved.source.File)
}

func (s *addDeprecationSuite) TestExplicitDeprecatedSelectorUsesBuiltin() {
	cmd := &AddCmd{GlobalFlags: &flags.GlobalFlags{}}
	resolved, err := cmd.resolveAddSource(
		context.Background(),
		[]string{providerpkg.DeprecatedMicrosandboxSource},
		"legacy-copy",
	)
	s.Require().NoError(err)
	s.Require().Equal(providerpkg.MicrosandboxDriver, resolved.source.Raw)
	s.Require().True(resolved.source.Internal)
	s.Require().Equal("legacy-copy", resolved.name)
}

func (s *addDeprecationSuite) TestAliasDownloadFailureDoesNotInstallOrChangeDefault() {
	s.T().Setenv("DEVSY_HOME", s.T().TempDir())
	s.T().Setenv("DEVSY_CONFIG", "")
	config.ResetPathManager()
	s.T().Cleanup(config.ResetPathManager)
	client := devsyhttp.GetHTTPClient()
	client.Transport = providerAddRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    req,
		}, nil
	})
	cmd := &AddCmd{GlobalFlags: &flags.GlobalFlags{}}
	devsyConfig := &config.Config{
		DefaultContext: config.DefaultContext,
		Contexts: map[string]*config.ContextConfig{
			config.DefaultContext: {
				DefaultProvider: "docker",
				Providers:       map[string]*config.ProviderConfig{},
			},
		},
	}
	s.Require().NoError(config.SaveConfig(devsyConfig))
	configPath, err := config.GetConfigPath()
	s.Require().NoError(err)
	before, err := os.ReadFile(filepath.Clean(configPath))
	s.Require().NoError(err)
	err = cmd.Run(context.Background(), devsyConfig, []string{providerpkg.MicrosandboxDriver})
	s.Require().Error(err)
	s.Require().NotContains(devsyConfig.Current().Providers, providerpkg.MicrosandboxDriver)
	s.Require().Equal("docker", devsyConfig.Current().DefaultProvider)
	after, err := os.ReadFile(filepath.Clean(configPath))
	s.Require().NoError(err)
	s.Require().Equal(before, after)
	_, err = providerpkg.LoadProviderConfig(config.DefaultContext, providerpkg.MicrosandboxDriver)
	s.Require().Error(err)
}

func providerAddResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

type providerAddRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip providerAddRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return roundTrip(req)
}
