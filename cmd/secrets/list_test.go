package secrets

import (
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	devsysecrets "github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/suite"
)

type ListTestSuite struct {
	suite.Suite
}

func TestListSuite(t *testing.T) {
	suite.Run(t, new(ListTestSuite))
}

type listTestStore struct {
	metas []devsysecrets.SecretMeta
}

func (s *listTestStore) Set(string, string, string, devsysecrets.Kind) error { return nil }
func (s *listTestStore) Get(string, string) (string, error)                  { return "", nil }
func (s *listTestStore) Meta(string, string) (devsysecrets.SecretMeta, error) {
	return devsysecrets.SecretMeta{}, nil
}
func (s *listTestStore) List(string) ([]devsysecrets.SecretMeta, error) { return s.metas, nil }
func (s *listTestStore) Delete(string, string) error                    { return nil }

func (s *ListTestSuite) TestListEntriesMarksAttachedSecrets() {
	created := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	store := &listTestStore{metas: []devsysecrets.SecretMeta{
		{
			Name:    "ATTACHED",
			Context: config.DefaultContext,
			Kind:    devsysecrets.KindSecret,
			Created: created,
		},
		{Name: "DETACHED", Context: config.DefaultContext, Kind: devsysecrets.KindSecret},
		{Name: "ENV_VAR", Context: config.DefaultContext, Kind: devsysecrets.KindEnv},
	}}
	cfg := deleteTestConfig([]string{"ATTACHED", "sops:project/API_TOKEN"})

	entries, err := listEntries(cfg, store, config.DefaultContext)
	s.Require().NoError(err)
	s.Require().Len(entries, 2)
	s.Require().Equal("ATTACHED", entries[0].Name)
	s.Require().True(entries[0].Attached)
	s.Require().Equal(created.Format(time.RFC3339), entries[0].Created)
	s.Require().Equal("DETACHED", entries[1].Name)
	s.Require().False(entries[1].Attached)
}

func (s *ListTestSuite) TestListEntriesDetachedWhenContextHasNoBindings() {
	store := &listTestStore{metas: []devsysecrets.SecretMeta{
		{Name: "TOKEN", Context: config.DefaultContext, Kind: devsysecrets.KindSecret},
	}}
	cfg := deleteTestConfig(nil)

	entries, err := listEntries(cfg, store, config.DefaultContext)
	s.Require().NoError(err)
	s.Require().Len(entries, 1)
	s.Require().False(entries[0].Attached)
}
