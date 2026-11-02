package provider

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type listDeprecationSuite struct {
	suite.Suite
}

const (
	testMicrosandboxName = provider.MicrosandboxDriver
	testLegacyName       = "legacy"
)

func TestListDeprecationSuite(t *testing.T) {
	suite.Run(t, new(listDeprecationSuite))
}

func (s *listDeprecationSuite) TestPlainListShowsLifecycleSeparately() {
	cmd := &ListCmd{}
	cfg := &config.Config{
		DefaultContext: config.DefaultContext,
		Contexts: map[string]*config.ContextConfig{
			config.DefaultContext: {DefaultProvider: testMicrosandboxName},
		},
	}
	entries := map[string]*workspace.ProviderWithOptions{
		testMicrosandboxName: {
			Config: &provider.ProviderConfig{
				Name:        testMicrosandboxName,
				Version:     "v1",
				Description: testLegacyName,
			},
			Deprecated: true,
		},
	}
	output := captureProviderListOutput(s.T(), func() error {
		return cmd.renderInstalledPlain(cfg, entries)
	})
	s.Require().NoError(output.err)
	s.Require().Contains(output.text, "Lifecycle")
	s.Require().Contains(output.text, "deprecated")
	s.Require().Contains(output.text, "true")
}

func (s *listDeprecationSuite) TestJSONListIncludesDeprecationMetadata() {
	cmd := &ListCmd{}
	cfg := &config.Config{
		DefaultContext: config.DefaultContext,
		Contexts: map[string]*config.ContextConfig{
			config.DefaultContext: {DefaultProvider: testLegacyName},
		},
	}
	entries := map[string]*workspace.ProviderWithOptions{
		testLegacyName: {Config: &provider.ProviderConfig{Name: testLegacyName}, Deprecated: true},
		"external":     {Config: &provider.ProviderConfig{Name: testMicrosandboxName}},
	}
	output := captureProviderListOutput(s.T(), func() error {
		return cmd.renderInstalledJSON(cfg, map[string]*config.ProviderConfig{}, entries)
	})
	s.Require().NoError(output.err)
	var result map[string]map[string]json.RawMessage
	s.Require().NoError(json.Unmarshal([]byte(output.text), &result))
	var legacyDeprecated, legacyDefault bool
	s.Require().NoError(json.Unmarshal(result[testLegacyName]["deprecated"], &legacyDeprecated))
	s.Require().NoError(json.Unmarshal(result[testLegacyName]["default"], &legacyDefault))
	s.Require().True(legacyDeprecated)
	s.Require().True(legacyDefault)
	s.Require().NotContains(result["external"], "deprecated")
	var externalName string
	var externalConfig map[string]json.RawMessage
	s.Require().NoError(json.Unmarshal(result["external"]["config"], &externalConfig))
	s.Require().NoError(json.Unmarshal(externalConfig["name"], &externalName))
	s.Require().Equal("microsandbox", externalName)
}

type providerListOutput struct {
	text string
	err  error
}

func captureProviderListOutput(t *testing.T, render func() error) (output providerListOutput) {
	t.Helper()
	capture, err := os.CreateTemp(t.TempDir(), "stdout")
	require.NoError(t, err)
	previous := os.Stdout
	os.Stdout = capture
	defer func() {
		os.Stdout = previous
		output.err = errors.Join(output.err, capture.Close())
	}()
	renderErr := render()
	_, seekErr := capture.Seek(0, io.SeekStart)
	data, readErr := io.ReadAll(capture)
	return providerListOutput{
		text: string(data),
		err:  errors.Join(renderErr, seekErr, readErr),
	}
}

func TestCaptureProviderListOutputLargeOutput(t *testing.T) {
	want := strings.Repeat("0123456789abcdef", 1<<16)
	renderErr := errors.New("render failed")
	previous := os.Stdout
	output := captureProviderListOutput(t, func() error {
		n, err := io.WriteString(os.Stdout, want)
		require.NoError(t, err)
		require.Equal(t, len(want), n)
		return renderErr
	})
	require.Equal(t, want, output.text)
	require.ErrorIs(t, output.err, renderErr)
	require.Same(t, previous, os.Stdout)
}
