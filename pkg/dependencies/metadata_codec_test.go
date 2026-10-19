package dependencies

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	metadataInvalidLink   = "symlink"
	metadataTraversalPath = "../outside"
)

type MetadataCodecSuite struct{ suite.Suite }

func TestMetadataCodecs(t *testing.T) { suite.Run(t, new(MetadataCodecSuite)) }

func metadataReceiptFixture() Receipt {
	return Receipt{
		SchemaVersion: MetadataSchemaVersion,
		Identity: ReceiptIdentity{
			ToolID:         "microsandbox",
			RuntimeVersion: "v0.7.7",
			Platform:       "linux/amd64",
			ArchiveSHA256:  strings.Repeat("a", 64),
		},
		RecipeID:     sourceRecordedCandidate().ID,
		RecipeSHA256: strings.Repeat("b", 64),
		SourceURL: "https://github.com/superradcompany/microsandbox/releases/download/" +
			"v0.7.7/microsandbox-linux-x86_64.tar.gz",
		InstalledAt: time.Date(
			2026,
			10,
			19,
			0,
			0,
			0,
			0,
			time.UTC,
		),
		Layout: "flat_verbatim_no_links",
		Components: []ReceiptComponent{
			{
				Path:   metadataExecutable,
				Type:   metadataRegular,
				SHA256: strings.Repeat("c", 64),
				Size:   1024,
			},
			{
				Path:   metadataFirmware,
				Type:   metadataRegular,
				SHA256: strings.Repeat("d", 64),
				Size:   4096,
			},
		},
		Validation: ReceiptValidation{Archive: "sha256_verified", Layout: "validated"},
	}
}

func metadataRegistryFixture() Registry {
	return Registry{
		SchemaVersion: MetadataSchemaVersion,
		Revision:      1,
		Receipts:      []ReceiptIdentity{metadataReceiptFixture().Identity},
	}
}

func metadataJournalFixture() Journal {
	receipt := metadataReceiptFixture()
	return Journal{
		SchemaVersion: MetadataSchemaVersion, OperationID: "operation-123", Phase: PhasePlanned,
		Artifacts: []ReceiptIdentity{receipt.Identity}, Receipts: []ReceiptIdentity{},
		StartedAt: receipt.InstalledAt, UpdatedAt: receipt.InstalledAt,
	}
}

func (s *MetadataCodecSuite) TestDeterministicRoundTrips() {
	receipt := metadataReceiptFixture()
	data, err := EncodeReceipt(receipt)
	s.Require().NoError(err)
	decoded, err := DecodeReceipt(bytes.NewReader(data))
	s.Require().NoError(err)
	s.Equal(receipt, decoded)
	again, err := EncodeReceipt(decoded)
	s.Require().NoError(err)
	s.Equal(data, again)
	decoded.Components[0].Path = "mutated-component-path"
	s.Equal(metadataExecutable, receipt.Components[0].Path)
	registry := metadataRegistryFixture()
	data, err = EncodeRegistry(registry)
	s.Require().NoError(err)
	decodedRegistry, err := DecodeRegistry(bytes.NewReader(data))
	s.Require().NoError(err)
	s.Equal(registry, decodedRegistry)
	again, err = EncodeRegistry(decodedRegistry)
	s.Require().NoError(err)
	s.Equal(data, again)
	journal := metadataJournalFixture()
	data, err = EncodeJournal(journal)
	s.Require().NoError(err)
	decodedJournal, err := DecodeJournal(bytes.NewReader(data))
	s.Require().NoError(err)
	s.Equal(journal, decodedJournal)
	again, err = EncodeJournal(decodedJournal)
	s.Require().NoError(err)
	s.Equal(data, again)
}

func (s *MetadataCodecSuite) TestReceiptPreservesCatalogRecipeID() {
	recipe := sourceRecordedCandidate()
	s.Require().NoError(validateRecipe(recipe))
	s.Equal("microsandbox-0.7.7-linux-amd64", recipe.ID)
	receipt := metadataReceiptFixture()
	receipt.RecipeID = recipe.ID
	s.Run("encode", func() {
		data, err := EncodeReceipt(receipt)
		s.Require().NoError(err)
		decoded, err := DecodeReceipt(bytes.NewReader(data))
		s.Require().NoError(err)
		s.Equal(receipt, decoded)
	})
	s.Run("decode", func() {
		data, err := json.Marshal(receipt)
		s.Require().NoError(err)
		decoded, err := DecodeReceipt(bytes.NewReader(data))
		s.Require().NoError(err)
		s.Equal(receipt, decoded)
	})
}

func (s *MetadataCodecSuite) TestReceiptRejectsMalformedRecipeIDs() {
	for _, id := range []string{
		"", ".", "..", metadataTraversalPath, "a/b", `a\b`, "C:ads", "Upper", "a b", "a\nb",
	} {
		s.Run(id, func() {
			recipe := sourceRecordedCandidate()
			recipe.ID = id
			s.Require().Error(validateRecipe(recipe))
			receipt := metadataReceiptFixture()
			receipt.RecipeID = id
			_, err := EncodeReceipt(receipt)
			s.Error(err)
			data, err := json.Marshal(receipt)
			s.Require().NoError(err)
			decoded, err := DecodeReceipt(bytes.NewReader(data))
			s.Error(err)
			s.Equal(Receipt{}, decoded)
		})
	}
}

func (s *MetadataCodecSuite) TestMalformedDocuments() {
	original, err := EncodeRegistry(metadataRegistryFixture())
	s.Require().NoError(err)
	cases := []string{
		"null",
		"[]",
		"{}",
		string(original) + " {}",
		string(original) + "garbage",
		strings.Replace(
			string(original),
			`"schemaVersion":1`,
			`"schemaVersion":1,"schemaVersion":1`,
			1,
		),
		strings.Replace(string(original), `"revision":1`, `"revision":1,"Revision":2`, 1),
		strings.Replace(string(original), `"revision":1`, `"revision":1,"unknown":true`, 1),
		strings.Replace(string(original), `"revision":1`, `"revision":null`, 1),
		strings.Replace(string(original), `"revision":1`, `"revision":-1`, 1),
		strings.Replace(string(original), `"revision":1`, `"revision":0`, 1),
		strings.Replace(string(original), `"receipts":[`, `"receipts":null,"other":[`, 1),
		strings.Replace(
			string(original),
			`"platform":"linux/amd64"`,
			`"platform":"linux/amd64","platform":"linux/amd64"`,
			1,
		),
		strings.Replace(
			string(original),
			`"platform":"linux/amd64"`,
			`"platform":"linux/amd64","revision":1`,
			1,
		),
		strings.Replace(string(original), `"schemaVersion":1`, `"schemaVersion":0`, 1),
	}
	for _, data := range cases {
		s.Run(data[:min(len(data), 80)], func() {
			decoded, err := DecodeRegistry(strings.NewReader(data))
			s.Error(err)
			s.Equal(Registry{}, decoded)
		})
	}
}

func (s *MetadataCodecSuite) TestFutureSchemaInspection() {
	for _, decode := range []func(io.Reader) error{
		func(r io.Reader) error { _, err := DecodeReceipt(r); return err },
		func(r io.Reader) error { _, err := DecodeRegistry(r); return err },
		func(r io.Reader) error { _, err := DecodeJournal(r); return err },
	} {
		err := decode(
			strings.NewReader(
				`{"schemaVersion":27,"unknownFuture":{"value":true,"nullable":null}}`,
			),
		)
		var future *UnsupportedMetadataSchemaError
		s.Require().ErrorAs(err, &future)
		s.Equal(27, future.Version)
		s.Contains(err.Error(), "read-only")
	}
	receipt := metadataReceiptFixture()
	receipt.SchemaVersion = 27
	_, err := EncodeReceipt(receipt)
	var future *UnsupportedMetadataSchemaError
	s.ErrorAs(err, &future)
}

func (s *MetadataCodecSuite) TestReceiptRejectsUntrustedContent() {
	cases := map[string]func(*Receipt){
		"archive digest":   func(r *Receipt) { r.Identity.ArchiveSHA256 = "abcd" },
		"recipe digest":    func(r *Receipt) { r.RecipeSHA256 = strings.Repeat("A", 64) },
		"file digest":      func(r *Receipt) { r.Components[0].SHA256 = "bad" },
		"size":             func(r *Receipt) { r.Components[0].Size = -1 },
		"link":             func(r *Receipt) { r.Components[0].Type = metadataInvalidLink },
		"script":           func(r *Receipt) { r.Components[0].Path = "install.sh" },
		"layout":           func(r *Receipt) { r.Layout = "bin_lib" },
		"duplicate":        func(r *Receipt) { r.Components[1] = r.Components[0] },
		"wrong platform":   func(r *Receipt) { r.Identity.Platform = "windows/amd64" },
		"validation":       func(r *Receipt) { r.Validation.Archive = "not_checked" },
		"userinfo":         func(r *Receipt) { r.SourceURL = "https://user:password@github.com/release" },
		"encoded userinfo": func(r *Receipt) { r.SourceURL = "https://user%3Apassword@github.com/release" },
		"query token":      func(r *Receipt) { r.SourceURL += "?token=secret" },
		"fragment":         func(r *Receipt) { r.SourceURL += "#secret" },
		"http":             func(r *Receipt) { r.SourceURL = strings.Replace(r.SourceURL, "https:", "http:", 1) },
	}
	for name, change := range cases {
		s.Run(name, func() {
			receipt := metadataReceiptFixture()
			change(&receipt)
			_, err := EncodeReceipt(receipt)
			s.Error(err)
			data, err := json.Marshal(receipt)
			s.Require().NoError(err)
			_, err = DecodeReceipt(bytes.NewReader(data))
			s.Error(err)
		})
	}
	paths := []string{
		metadataTraversalPath, "/msb", "bin/msb", `C:\msb`, `\\server\msb`, "msb:stream",
		"CON", "NUL", "msb.", "libkrunfw.so", "current", "./msb",
	}
	for _, path := range paths {
		receipt := metadataReceiptFixture()
		receipt.Components[0].Path = path
		data, err := json.Marshal(receipt)
		s.Require().NoError(err)
		_, err = DecodeReceipt(bytes.NewReader(data))
		s.Error(err, path)
	}
}

func (s *MetadataCodecSuite) TestRegistryIdentityAndRevision() {
	registry := metadataRegistryFixture()
	other := registry.Receipts[0]
	other.ArchiveSHA256 = strings.Repeat("e", 64)
	registry.Receipts = append(registry.Receipts, other)
	_, err := EncodeRegistry(registry)
	s.NoError(err, "same version, distinct full digest is a distinct bundle")
	registry.Receipts = append(registry.Receipts, other)
	_, err = EncodeRegistry(registry)
	s.Error(err)
	registry = metadataRegistryFixture()
	registry.Revision = 0
	_, err = EncodeRegistry(registry)
	s.Error(err)
	registry.Revision = 1
	registry.Receipts = []ReceiptIdentity{}
	_, err = EncodeRegistry(registry)
	s.NoError(err)
}

func (s *MetadataCodecSuite) TestJournalPhasesAndTerminalData() {
	phases := []JournalPhase{
		PhasePlanned,
		PhaseDownloading,
		PhaseVerifiedArchive,
		PhaseExtracting,
		PhasePublished,
		PhaseBinding,
	}
	for _, phase := range phases {
		journal := metadataJournalFixture()
		journal.Phase = phase
		_, err := EncodeJournal(journal)
		s.NoError(err)
		journal.Terminal = &JournalTerminal{At: journal.UpdatedAt, ReasonCode: "complete"}
		_, err = EncodeJournal(journal)
		s.Error(err)
	}
	reasons := map[JournalPhase]string{
		PhaseComplete:    "complete",
		PhaseCanceled:    "canceled",
		PhaseFailed:      "dependency_integrity_failed",
		PhaseInterrupted: "dependency_operation_interrupted",
	}
	for phase, reason := range reasons {
		journal := metadataJournalFixture()
		journal.Phase = phase
		_, err := EncodeJournal(journal)
		s.Error(err)
		journal.Terminal = &JournalTerminal{At: journal.UpdatedAt, ReasonCode: reason}
		data, err := EncodeJournal(journal)
		s.Require().NoError(err)
		decoded, err := DecodeJournal(bytes.NewReader(data))
		s.Require().NoError(err)
		s.Equal(journal, decoded)
		journal.Terminal.ReasonCode = "password=secret"
		_, err = EncodeJournal(journal)
		s.Error(err)
	}
	for _, id := range []string{
		"..", metadataTraversalPath, "C:ads", "con", "nul", "com1", "lpt9", "a/b", `a\b`,
		sourceRecordedCandidate().ID, "operation_123",
	} {
		journal := metadataJournalFixture()
		journal.OperationID = id
		_, err := EncodeJournal(journal)
		s.Error(err, id)
	}
}

type boundedMetadataReader struct{ remaining int }

func (r *boundedMetadataReader) Read(data []byte) (int, error) {
	if len(data) > r.remaining {
		return 0, errors.New("reader consumed beyond metadata bound")
	}
	for i := range data {
		data[i] = ' '
	}
	r.remaining -= len(data)
	return len(data), nil
}

func TestMetadataReadBound(t *testing.T) {
	reader := &boundedMetadataReader{remaining: maxMetadataBytes + 1}
	_, err := DecodeRegistry(reader)
	require.ErrorContains(t, err, "size limit")
	require.Zero(t, reader.remaining)
}
