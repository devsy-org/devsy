package dependency

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/dependencies"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/devsy/providers"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	inspectTestDocker = "docker"
	inspectTestPodman = "podman"
	snapshotSecond    = "second"

	inspectTestContext      = "chosen"
	inspectTestInstallation = "renamed"
	inspectTestPATH         = "PATH"
	inspectTestDockerPath   = "DOCKER_PATH"
	inspectTestOther        = "other"
	inspectTestCommand      = "inspect"
	snapshotFirst           = "first"
)

type inspectSuite struct {
	suite.Suite
	root     string
	paths    config.PathManager
	snapshot *config.Config
	manifest *provider.ProviderConfig
	metadata dependencies.PassiveReportMetadata
}

func TestInspect(t *testing.T) { suite.Run(t, new(inspectSuite)) }

func (s *inspectSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.T().Setenv(config.EnvConfig, "")
	s.paths = config.NewReadOnlyPathManagerForHome(filepath.Join(s.root, "explicit"))
	s.snapshot = &config.Config{
		SchemaVersion:  1,
		DefaultContext: inspectTestContext,
		Contexts: map[string]*config.ContextConfig{
			inspectTestContext: {
				DefaultProvider: inspectTestInstallation,
				Providers:       map[string]*config.ProviderConfig{inspectTestInstallation: {}},
			},
		},
	}
	s.manifest = s.install(inspectTestDocker, inspectTestInstallation)
	s.saveConfig()
	s.metadata = dependencies.PassiveReportMetadata{
		EvaluatedAt:              time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC),
		VirtualizationCapability: "not_required",
		Redact:                   func(value string) string { return value },
	}
}

func (s *inspectSuite) TestAllBuiltinsAndSavedPaths() {
	for _, test := range []struct{ builtin, tool, option string }{
		{inspectTestDocker, inspectTestDocker, inspectTestDockerPath},
		{inspectTestPodman, inspectTestPodman, "PODMAN_PATH"},
		{"apple", "apple-container", "CONTAINER_PATH"},
		{"kubernetes", "kubernetes-config", "KUBERNETES_CONFIG"},
	} {
		s.Run(test.builtin, func() {
			s.install(test.builtin, inspectTestInstallation)
			for _, user := range []bool{false, true} {
				for _, path := range []string{"", filepath.Join(s.root, "absent"), test.builtin} {
					s.snapshot.Current().Providers[inspectTestInstallation].Options = map[string]config.OptionValue{
						test.option: {Value: path, UserProvided: user},
					}
					s.saveConfig()
					before, err := dependencytest.Tree(s.root)
					s.Require().NoError(err)
					report, err := s.inspect(test.tool, "", false)
					s.Require().NoError(err)
					after, err := dependencytest.Tree(s.root)
					s.Require().NoError(err)
					s.Equal(before, after)
					s.Equal(test.tool, report.Items[0].ID)
					s.Equal(inspectTestInstallation, report.Target.ProviderName)
					s.Equal(test.builtin, report.Target.ProviderType)
					s.Equal("unknown", report.Compatibility)
					s.Equal("not_checked", report.Reachability)
					s.Equal("not_required", report.Host.VirtualizationCapability)
					s.Equal(runtime.GOOS, report.Host.GOOS)
					s.Empty(report.Actions)
					s.False(report.Executed)
					s.Regexp("^[0-9a-f]{64}$", report.Target.ConfigRevision)
					s.Regexp("^[0-9a-f]{64}$", report.Target.ProviderConfigRevision)
				}
			}
		})
	}
}

func (s *inspectSuite) TestSavedOptionsIgnoreEnvironmentOverrides() {
	bin := filepath.Join(s.root, "bin")
	s.Require().NoError(os.Mkdir(bin, 0o700))
	executable := filepath.Join(bin, inspectTestDocker)
	marker := filepath.Join(s.root, "EXECUTED")
	s.Require().NoError(os.WriteFile(executable, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o600))
	// #nosec G302 -- test-owned sentinel must be executable.
	s.Require().NoError(os.Chmod(executable, 0o700))
	s.snapshot.Current().Providers[inspectTestInstallation].Options = map[string]config.OptionValue{
		inspectTestDockerPath: {Value: filepath.Join(s.root, "saved-missing"), UserProvided: true},
		// #nosec G101 -- synthetic endpoint credentials must remain inert and absent from output.
		"DOCKER_HOST": {Value: "https://user:credential@127.0.0.1:1"},
	}
	s.saveConfig()
	dependencytest.Preserve(s.T(), s.root)
	manager := config.DefaultPathManager()
	report, err := inspectSavedProvider(
		s.ctx(),
		inspectionRequest{
			Tool:               inspectTestDocker,
			ForProvider:        "",
			ForProviderPresent: false,
			Environment: map[string]string{
				inspectTestPATH:                      bin,
				inspectTestDockerPath:                executable,
				"DEVSY_PROVIDER_RENAMED_DOCKER_PATH": executable,
			},
			WorkingDirectory: s.root,
			Metadata:         s.metadata,
		},
	)
	s.Require().NoError(err)
	s.Equal("missing", report.Availability)
	s.Require().Len(report.Items[0].Candidates, 1)
	s.Equal(filepath.Join(s.root, "saved-missing"), report.Items[0].Candidates[0].LexicalPath)
	s.NoFileExists(marker)
	s.Same(manager, config.DefaultPathManager())
	s.snapshot.Current().Providers[inspectTestInstallation].Options = nil
	report, err = inspectSavedProvider(
		s.ctx(),
		inspectionRequest{
			Tool:               inspectTestDocker,
			ForProvider:        "",
			ForProviderPresent: false,
			Environment:        map[string]string{inspectTestPATH: bin},
			WorkingDirectory:   s.root,
			Metadata:           s.metadata,
		},
	)
	s.Require().NoError(err)
	s.Equal("unknown", report.Availability)
	s.NoFileExists(marker)
}

func (s *inspectSuite) TestSelectionAndCLIProvenance() {
	s.install(inspectTestPodman, inspectTestOther)
	s.snapshot.Current().Providers[inspectTestOther] = &config.ProviderConfig{}
	s.saveConfig()
	before, err := json.Marshal(s.snapshot)
	s.Require().NoError(err)
	report, err := s.inspect(inspectTestPodman, inspectTestOther, true)
	s.Require().NoError(err)
	s.Equal(inspectTestOther, report.Target.ProviderName)
	after, err := json.Marshal(s.snapshot)
	s.Require().NoError(err)
	s.Equal(before, after)
	_, err = s.inspect(inspectTestDocker, "", true)
	s.EqualError(err, "dependency inspect requires a nonempty --for-provider")
	for _, value := range []string{inspectTestInstallation, ""} {
		ctx := flags.WithInspectionInvocation(
			s.ctx(),
			flags.InspectionInvocation{ProviderPresent: true, Provider: value},
		)
		_, err = inspectSavedProvider(
			ctx,
			inspectionRequest{
				Tool:               inspectTestPodman,
				ForProvider:        inspectTestOther,
				ForProviderPresent: true,
				Environment:        map[string]string{},
				WorkingDirectory:   s.root,
				Metadata:           s.metadata,
			},
		)
		s.EqualError(err, "--provider conflicts with the dependency inspection target")
	}
	s.snapshot.Current().DefaultProvider = inspectTestOther
	report, err = s.inspect(inspectTestPodman, "", false)
	s.Require().NoError(err)
	s.Equal(inspectTestOther, report.Target.ProviderName)
}

func (s *inspectSuite) TestPolicyFailures() {
	for _, test := range []struct {
		name   string
		change func()
	}{
		{"modified", func() { s.manifest.Description += " changed"; s.writeManifest(s.manifest) }},
		{"custom", func() {
			s.manifest.Source = provider.ProviderSource{File: "private-secret"}
			s.writeManifest(s.manifest)
		}},
		{"external", func() {
			// #nosec G101 -- synthetic untrusted source credential.
			s.manifest.Source = provider.ProviderSource{URL: "https://user:secret@example.test/provider"}
			s.writeManifest(s.manifest)
		}},
		{"ambiguous", func() { s.manifest.Source.URL = "https://secret@example.test"; s.writeManifest(s.manifest) }},
		{"wrong-installation", func() {
			dir, err := s.paths.ProviderDir(inspectTestContext, inspectTestInstallation)
			s.Require().NoError(err)
			s.manifest.Name = "wrong"
			raw, err := json.Marshal(s.manifest)
			s.Require().NoError(err)
			s.Require().NoError(os.WriteFile(filepath.Join(dir, provider.ProviderConfigFile), raw, 0o600))
		}},
		{"missing-manifest", func() {
			dir, err := s.paths.ProviderDir(inspectTestContext, inspectTestInstallation)
			s.Require().NoError(err)
			s.Require().NoError(os.Remove(filepath.Join(dir, provider.ProviderConfigFile)))
		}},
		{"missing-record", func() { delete(s.snapshot.Current().Providers, inspectTestInstallation) }},
		{"dynamic", func() {
			s.snapshot.Current().Providers[inspectTestInstallation].DynamicOptions = config.OptionDefinitions{
				inspectTestDockerPath: &types.Option{Command: "secret-command"},
			}
		}},
	} {
		s.Run(test.name, func() {
			s.SetupTest()
			test.change()
			dependencytest.Preserve(s.T(), s.root)
			report, err := s.inspect(inspectTestDocker, "", false)
			s.Require().Error(err)
			s.Empty(report.Schema)
			s.Equal(policyBlocked, clierr.Classify(err).Code)
			s.NotContains(err.Error(), "secret")
		})
	}
}

func (s *inspectSuite) TestMismatchPrecedesLookupAndRequiresInputs() {
	// A nonexistent cwd would fail preparation if it required filesystem lookup.
	_, err := inspectSavedProvider(
		s.ctx(),
		inspectionRequest{
			Tool:               inspectTestPodman,
			ForProvider:        "",
			ForProviderPresent: false,
			Environment:        map[string]string{},
			WorkingDirectory:   filepath.Join(s.root, "no-cwd"),
			Metadata:           s.metadata,
		},
	)
	s.EqualError(err, "tool does not match the installed provider dependency")
	_, err = inspectSavedProvider(
		s.T().Context(),
		inspectionRequest{
			Tool:               inspectTestDocker,
			ForProvider:        "",
			ForProviderPresent: false,
			Environment:        nil,
			WorkingDirectory:   s.root,
			Metadata:           s.metadata,
		},
	)
	s.EqualError(err, "dependency inspection requires prepared passive inputs")
	ctx, cancel := context.WithCancel(s.ctx())
	cancel()
	_, err = inspectSavedProvider(
		ctx,
		inspectionRequest{
			Tool:               inspectTestDocker,
			ForProvider:        "",
			ForProviderPresent: false,
			Environment:        nil,
			WorkingDirectory:   s.root,
			Metadata:           s.metadata,
		},
	)
	s.ErrorIs(err, context.Canceled)
}

func (s *inspectSuite) TestCommandOutput() {
	s.T().Setenv(inspectTestPATH, "")
	for _, args := range [][]string{
		{inspectTestCommand, inspectTestDocker, "--json"},
		{inspectTestCommand, inspectTestDocker, "--json=false"},
	} {
		command := NewDependencyCmd(&flags.GlobalFlags{ResultFormat: "json"})
		command.SetContext(s.ctx())
		var out bytes.Buffer
		command.SetOut(&out)
		command.SetArgs(args)
		s.Require().NoError(command.Execute())
		decoder := json.NewDecoder(&out)
		decoder.DisallowUnknownFields()
		var report dependencies.Report
		s.Require().NoError(decoder.Decode(&report))
		s.ErrorIs(decoder.Decode(new(any)), io.EOF)
		s.Equal("devsy.dependencies.report.v1", report.Schema)
	}
	command := NewDependencyCmd(&flags.GlobalFlags{ResultFormat: "plain"})
	command.SetContext(s.ctx())
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetArgs([]string{inspectTestCommand, inspectTestDocker, "--json=false"})
	s.Require().NoError(command.Execute())
	s.Contains(out.String(), "Docker:")
	s.Contains(out.String(), "compatibility: unknown; reachability: not_checked")
}

func (s *inspectSuite) TestConstructorAndRedactedCandidate() {
	dependencytest.Preserve(s.T(), s.root)
	NewDependencyCmd(&flags.GlobalFlags{})
	s.snapshot.Current().Providers[inspectTestInstallation].Options = map[string]config.OptionValue{
		inspectTestDockerPath: {
			Value:        filepath.Join(s.root, "private-candidate"),
			UserProvided: true,
		},
	}
	s.metadata.Redact = func(value string) string {
		if strings.Contains(value, "private-candidate") {
			return "***"
		}
		return value
	}
	report, err := s.inspect(inspectTestDocker, "", false)
	s.Require().NoError(err)
	s.Empty(report.Items[0].Candidates)
	raw, err := json.Marshal(report)
	s.Require().NoError(err)
	s.NotContains(string(raw), "private-candidate")
}

func TestSnapshotRevisions(t *testing.T) {
	left := map[string]any{"z": []string{snapshotSecond, snapshotFirst}, "a": "value"}
	right := map[string]any{"a": "value", "z": []string{snapshotSecond, snapshotFirst}}
	a, err := snapshotRevision(left)
	require.NoError(t, err)
	b, err := snapshotRevision(right)
	require.NoError(t, err)
	require.Equal(t, a, b)
	right["z"] = []string{snapshotFirst, snapshotSecond}
	b, err = snapshotRevision(right)
	require.NoError(t, err)
	require.NotEqual(t, a, b)
	manifest := provider.ProviderConfig{Name: "installed", Description: "metadata"}
	record := providerSnapshotV1{Version: 1, Manifest: manifest}
	a, err = snapshotRevision(record)
	require.NoError(t, err)
	record.StoredOptions = map[string]config.OptionValue{}
	b, err = snapshotRevision(record)
	require.NoError(t, err)
	require.NotEqual(t, a, b, "record retains nil versus empty")
	for _, change := range []func(){
		func() { record.Manifest.Description = inspectTestOther },
		func() { record.StoredOptions[inspectTestPATH] = config.OptionValue{Value: "saved", UserProvided: true} },
		func() {
			record.DynamicOptions = config.OptionDefinitions{inspectTestPATH: &types.Option{Default: "dynamic"}}
		},
	} {
		a, err = snapshotRevision(record)
		require.NoError(t, err)
		change()
		b, err = snapshotRevision(record)
		require.NoError(t, err)
		require.NotEqual(t, a, b)
	}
	cfg := &config.Config{DefaultContext: inspectTestContext, Origin: "private"}
	a, err = snapshotRevision(cfg)
	require.NoError(t, err)
	cfg.Origin, cfg.OriginalContext = "different", "original"
	b, err = snapshotRevision(cfg)
	require.NoError(t, err)
	require.Equal(t, a, b)
	cfg.DefaultContext = "changed"
	b, err = snapshotRevision(cfg)
	require.NoError(t, err)
	require.NotEqual(t, a, b)
	require.Equal(
		t,
		map[string]string{inspectTestPATH: "last", "Path": "ambiguous"},
		inspectionEnvironment([]string{"PATH=first", "PATH=last", "Path=ambiguous"}),
	)
}

func (s *inspectSuite) install(builtin, name string) *provider.ProviderConfig {
	manifest, err := provider.ParseProvider(
		strings.NewReader(providers.GetBuiltInProviders()[builtin]),
	)
	s.Require().NoError(err)
	manifest.Name = name
	manifest.Source = provider.ProviderSource{Internal: true, Raw: builtin}
	s.writeManifest(manifest)
	return manifest
}

func (s *inspectSuite) writeManifest(manifest *provider.ProviderConfig) {
	dir, err := s.paths.ProviderDir(inspectTestContext, manifest.Name)
	s.Require().NoError(err)
	s.Require().NoError(os.MkdirAll(dir, 0o700))
	raw, err := json.Marshal(manifest)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(filepath.Join(dir, provider.ProviderConfigFile), raw, 0o600))
}

func (s *inspectSuite) saveConfig() {
	path, err := s.paths.ConfigFilePath()
	s.Require().NoError(err)
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o700))
	raw, err := json.Marshal(s.snapshot)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(path, raw, 0o600))
	s.snapshot, err = config.LoadConfigWithPathManager(s.paths, inspectTestContext, "")
	s.Require().NoError(err)
}

func (s *inspectSuite) ctx() context.Context {
	return flags.WithPassiveInputs(
		s.T().Context(),
		flags.PassiveInputs{Paths: s.paths, Config: s.snapshot},
	)
}

func (s *inspectSuite) inspect(tool, override string, present bool) (dependencies.Report, error) {
	return inspectSavedProvider(
		s.ctx(),
		inspectionRequest{
			Tool:               tool,
			ForProvider:        override,
			ForProviderPresent: present,
			Environment:        map[string]string{inspectTestPATH: ""},
			WorkingDirectory:   s.root,
			Metadata:           s.metadata,
		},
	)
}
