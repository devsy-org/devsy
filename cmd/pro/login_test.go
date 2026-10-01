package pro

import (
	"context"
	"testing"
	"time"

	rootflags "github.com/devsy-org/devsy/cmd/flags"
	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/suite"
)

const testProProviderName = "pro"

type loginSuite struct {
	suite.Suite
}

func TestLoginSuite(t *testing.T) {
	suite.Run(t, new(loginSuite))
}

func (s *loginSuite) SetupTest() {
	home := s.T().TempDir()
	s.T().Setenv("HOME", home)
	s.T().Setenv("USERPROFILE", home)
	config.ResetPathManager()
	s.Require().NoError(config.SaveConfig(&config.Config{
		DefaultContext: "default",
		Contexts: map[string]*config.ContextConfig{
			"default": {Providers: map[string]*config.ProviderConfig{}},
		},
	}))
}

func (s *loginSuite) TearDownTest() {
	config.ResetPathManager()
}

func (s *loginSuite) TestConcurrentLoginFindsCreatedInstance() {
	lock, err := provider.GetProInstanceOperationLock("default", "pro.example")
	s.Require().NoError(err)
	s.Require().NoError(lock.Lock())
	s.T().Cleanup(func() { _ = lock.Unlock() })

	cmd := &LoginCmd{GlobalFlags: proflags.GlobalFlags{GlobalFlags: &rootflags.GlobalFlags{}}}
	result := make(chan error, 1)
	go func() {
		_, err := cmd.prepareProvider(context.Background(), "https://pro.example")
		result <- err
	}()
	select {
	case err := <-result:
		s.FailNow("login completed during instance creation", err)
	case <-time.After(50 * time.Millisecond):
	}

	s.Require().NoError(provider.SaveProInstanceConfig("default", &provider.ProInstance{
		Provider: testProProviderName,
		Host:     "pro.example",
	}))
	s.Require().NoError(lock.Unlock())
	s.Require().NoError(<-result)
	s.Equal(testProProviderName, cmd.Provider)
}

func (s *loginSuite) TestDifferentHostDoesNotWaitForLoginLock() {
	lock, err := provider.GetProInstanceOperationLock("default", "first.example")
	s.Require().NoError(err)
	s.Require().NoError(lock.Lock())
	defer func() { _ = lock.Unlock() }()
	s.Require().NoError(provider.SaveProInstanceConfig("default", &provider.ProInstance{
		Provider: "other",
		Host:     "second.example",
	}))

	cmd := &LoginCmd{GlobalFlags: proflags.GlobalFlags{GlobalFlags: &rootflags.GlobalFlags{}}}
	result := make(chan error, 1)
	go func() {
		_, err := cmd.prepareProvider(context.Background(), "https://second.example")
		result <- err
	}()
	select {
	case err := <-result:
		s.Require().NoError(err)
	case <-time.After(time.Second):
		s.FailNow("unrelated host waited for the first host's login lock")
	}
	s.Equal("other", cmd.Provider)
}

func (s *loginSuite) TestHostLockDoesNotConflictWithProviderLock() {
	hostLock, err := provider.GetProInstanceOperationLock("default", "pro.example")
	s.Require().NoError(err)
	providerLock, err := provider.GetProviderOperationLock("default", "pro-login")
	s.Require().NoError(err)
	s.Require().NoError(hostLock.Lock())
	defer func() { _ = hostLock.Unlock() }()
	acquired, err := providerLock.TryLock()
	s.Require().NoError(err)
	s.Require().True(acquired)
	s.Require().NoError(providerLock.Unlock())
}

func (s *loginSuite) TestEquivalentHostsShareLock() {
	first, err := provider.GetProInstanceOperationLock("default", "Foo.Example.com")
	s.Require().NoError(err)
	second, err := provider.GetProInstanceOperationLock("default", "foo.example.com")
	s.Require().NoError(err)
	s.Equal(first.Path(), second.Path())
}

func (s *loginSuite) TestEquivalentHostFindsExistingInstance() {
	s.Require().NoError(provider.SaveProInstanceConfig("default", &provider.ProInstance{
		Provider: testProProviderName,
		Host:     "Foo.Example.com",
	}))
	cmd := &LoginCmd{GlobalFlags: proflags.GlobalFlags{GlobalFlags: &rootflags.GlobalFlags{}}}
	_, err := cmd.prepareProvider(context.Background(), "https://foo.example.com")
	s.Require().NoError(err)
	s.Equal(testProProviderName, cmd.Provider)
}

func (s *loginSuite) TestDistinctHostsWithSameInstanceIDAreRejected() {
	s.Require().NoError(provider.SaveProInstanceConfig("default", &provider.ProInstance{
		Provider: testProProviderName,
		Host:     "foo.example.com",
	}))
	cmd := &LoginCmd{GlobalFlags: proflags.GlobalFlags{GlobalFlags: &rootflags.GlobalFlags{}}}
	_, err := cmd.prepareProvider(context.Background(), "https://foo-example.com")
	s.Require().ErrorContains(err, "conflicts with existing host")
	stored, err := provider.LoadProInstanceConfig("default", "foo.example.com")
	s.Require().NoError(err)
	s.Equal("foo.example.com", stored.Host)
}
