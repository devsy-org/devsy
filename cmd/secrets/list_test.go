package secrets

import (
	"encoding/json"
	"io"
	"os"
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
	metas       []devsysecrets.SecretMeta
	inspections []devsysecrets.SecretInspection
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

func (s *listTestStore) Inspect(string) ([]devsysecrets.SecretInspection, error) {
	if s.inspections != nil {
		return s.inspections, nil
	}
	result := make([]devsysecrets.SecretInspection, 0, len(s.metas))
	for _, meta := range s.metas {
		result = append(
			result,
			devsysecrets.SecretInspection{Meta: meta, Availability: devsysecrets.SecretAvailable},
		)
	}
	return result, nil
}

func (s *ListTestSuite) TestListEntriesReportsAvailabilityInJSONAndPlainOutput() {
	store := &listTestStore{inspections: []devsysecrets.SecretInspection{
		{
			Meta: devsysecrets.SecretMeta{
				Name:    "LOCKED_FILE",
				Context: config.DefaultContext,
				Kind:    devsysecrets.KindSecret,
				Backend: devsysecrets.BackendFile,
			},
			Availability: devsysecrets.SecretLocked,
			ReasonCode:   "unlock_required",
		},
		{
			Meta: devsysecrets.SecretMeta{
				Name:    "AVAILABLE_KEYRING",
				Context: config.DefaultContext,
				Kind:    devsysecrets.KindSecret,
				Backend: devsysecrets.BackendKeyring,
			},
			Availability: devsysecrets.SecretAvailable,
		},
		{
			Meta: devsysecrets.SecretMeta{
				Name:    "MISSING_KEYRING",
				Context: config.DefaultContext,
				Kind:    devsysecrets.KindSecret,
				Backend: devsysecrets.BackendKeyring,
			},
			Availability: devsysecrets.SecretMissing,
			ReasonCode:   "secret_not_found",
		},
	}}
	entries, err := listEntries(deleteTestConfig(nil), store, config.DefaultContext)
	s.Require().NoError(err)
	s.Require().Len(entries, 3)
	s.Equal(devsysecrets.SecretLocked, entries[0].Availability)
	s.Equal(devsysecrets.SecretAvailable, entries[1].Availability)
	s.Equal(devsysecrets.SecretMissing, entries[2].Availability)

	jsonOutput := captureListOutput(s.T(), func() { s.Require().NoError(renderJSON(entries)) })
	var decoded []map[string]any
	s.Require().NoError(json.Unmarshal([]byte(jsonOutput), &decoded))
	s.Require().Len(decoded, 3)
	s.Equal("locked", decoded[0]["availability"])
	s.Equal("file", decoded[0]["backend"])
	s.Equal("unlock_required", decoded[0]["reasonCode"])
	s.Equal("available", decoded[1]["availability"])
	s.Equal("keyring", decoded[1]["backend"])
	s.Equal("missing", decoded[2]["availability"])

	plainOutput := captureListOutput(s.T(), func() { renderPlain(entries) })
	s.Contains(plainOutput, "Locked")
	s.Contains(plainOutput, "Available")
	s.Contains(plainOutput, "Missing")
}

func captureListOutput(t *testing.T, render func()) string {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = write
	defer func() { os.Stdout = previous }()
	render()
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	if err := read.Close(); err != nil {
		t.Fatal(err)
	}
	return string(output)
}
