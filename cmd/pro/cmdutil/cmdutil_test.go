package cmdutil

import (
	"context"
	"errors"
	"io"
	"testing"

	rootflags "github.com/devsy-org/devsy/cmd/flags"
	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/suite"
)

const testCmdName = "test"

type hostFlagSuite struct {
	suite.Suite
}

func TestHostFlagSuite(t *testing.T) {
	suite.Run(t, new(hostFlagSuite))
}

func newHostTestCmd(dest *string) *cobra.Command {
	c := &cobra.Command{
		Use:  testCmdName,
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	AddHostFlag(c, dest)
	return c
}

func (s *hostFlagSuite) TestRequiredWhenNeitherFlagNorEnvSet() {
	s.T().Setenv(cliflags.EnvName(names.Host), "")

	var host string
	c := newHostTestCmd(&host)
	c.SetArgs([]string{})

	s.EqualError(c.Execute(), `required flag(s) "host" not set`)
}

func (s *hostFlagSuite) TestEnvSatisfiesRequiredAndSetsDest() {
	s.T().Setenv(cliflags.EnvName(names.Host), "env-host")

	var host string
	c := newHostTestCmd(&host)
	c.SetArgs([]string{})

	s.NoError(c.Execute())
	s.Equal("env-host", host)
}

func (s *hostFlagSuite) TestFlagValueSetsDest() {
	s.T().Setenv(cliflags.EnvName(names.Host), "")

	var host string
	c := newHostTestCmd(&host)
	c.SetArgs([]string{"--" + names.Host, "flag-host"})

	s.NoError(c.Execute())
	s.Equal("flag-host", host)
}

func (s *hostFlagSuite) TestHostRunEResolvesHostAtRunTime() {
	orig := findPlatformProviderFn
	s.T().Cleanup(func() { findPlatformProviderFn = orig })

	var gotContext, gotProvider, gotHost string
	devsyConfig := &config.Config{}
	pCfg := &providerpkg.ProviderConfig{Name: "p"}
	findPlatformProviderFn = func(
		_ context.Context,
		ctxName, providerName, host string,
	) (*config.Config, *providerpkg.ProviderConfig, error) {
		gotContext, gotProvider, gotHost = ctxName, providerName, host
		return devsyConfig, pCfg, nil
	}

	gf := &proflags.GlobalFlags{
		GlobalFlags: &rootflags.GlobalFlags{Context: "ctx-a", Provider: "prov-b"},
	}
	var host string
	var ranConfig *config.Config
	var ranProvider *providerpkg.ProviderConfig
	run := func(_ context.Context, dc *config.Config, pc *providerpkg.ProviderConfig) error {
		ranConfig, ranProvider = dc, pc
		return nil
	}

	c := &cobra.Command{Use: testCmdName, RunE: HostRunE(gf, &host, run)}
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	AddHostFlag(c, &host)
	c.SetArgs([]string{"--" + names.Host, "late-host"})

	s.Require().NoError(c.Execute())
	s.Equal("ctx-a", gotContext)
	s.Equal("prov-b", gotProvider)
	s.Equal("late-host", gotHost)
	s.Same(devsyConfig, ranConfig)
	s.Same(pCfg, ranProvider)
}

func (s *hostFlagSuite) TestHostRunEPropagatesResolveErrorWithoutRunning() {
	orig := findPlatformProviderFn
	s.T().Cleanup(func() { findPlatformProviderFn = orig })

	boom := errors.New("resolve failed")
	findPlatformProviderFn = func(
		context.Context,
		string, string, string,
	) (*config.Config, *providerpkg.ProviderConfig, error) {
		return nil, nil, boom
	}

	ran := false
	run := func(context.Context, *config.Config, *providerpkg.ProviderConfig) error {
		ran = true
		return nil
	}

	var host string
	gf := &proflags.GlobalFlags{GlobalFlags: &rootflags.GlobalFlags{}}
	c := &cobra.Command{Use: testCmdName, RunE: HostRunE(gf, &host, run)}
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	AddHostFlag(c, &host)
	c.SetArgs([]string{"--" + names.Host, "h"})

	s.ErrorIs(c.Execute(), boom)
	s.False(ran)
}

func (s *hostFlagSuite) TestMarkAgentExecutedSetsRootAnnotation() {
	root := &cobra.Command{Use: "root", PersistentPreRun: MarkAgentExecuted}
	child := &cobra.Command{
		Use: "child",
		RunE: func(*cobra.Command, []string) error {
			return nil
		},
	}
	root.AddCommand(child)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"child"})

	s.Require().NoError(root.Execute())
	s.Equal("true", root.Annotations[config.AgentExecutedAnnotation])
}

func (s *hostFlagSuite) TestMarkAgentExecutedKeepsExistingRootAnnotations() {
	root := &cobra.Command{Use: "root"}
	root.Annotations = map[string]string{"other": "value"}

	MarkAgentExecuted(root, nil)

	s.Equal("true", root.Annotations[config.AgentExecutedAnnotation])
	s.Equal("value", root.Annotations["other"])
}
