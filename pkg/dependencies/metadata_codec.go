package dependencies

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	MetadataSchemaVersion = 1
	maxMetadataBytes      = 1 << 20
	maxMetadataReferences = 1024
)

// UnsupportedMetadataSchemaError permits read-only version inspection. No
// partially decoded document is returned, and writers must preserve the source.
type UnsupportedMetadataSchemaError struct{ Version int }

func (e *UnsupportedMetadataSchemaError) Error() string {
	return fmt.Sprintf(
		"dependency metadata schema %d is unsupported; read-only inspection only",
		e.Version,
	)
}

func EncodeReceipt(receipt Receipt) ([]byte, error) {
	return encodeMetadata(receipt.SchemaVersion, receipt)
}

func DecodeReceipt(reader io.Reader) (Receipt, error) {
	var receipt Receipt
	err := decodeMetadata(reader, &receipt)
	if err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func EncodeRegistry(registry Registry) ([]byte, error) {
	return encodeMetadata(registry.SchemaVersion, registry)
}

func DecodeRegistry(reader io.Reader) (Registry, error) {
	var registry Registry
	err := decodeMetadata(reader, &registry)
	if err != nil {
		return Registry{}, err
	}
	return registry, nil
}

func EncodeJournal(journal Journal) ([]byte, error) {
	return encodeMetadata(journal.SchemaVersion, journal)
}

func DecodeJournal(reader io.Reader) (Journal, error) {
	var journal Journal
	err := decodeMetadata(reader, &journal)
	if err != nil {
		return Journal{}, err
	}
	return journal, nil
}

func encodeMetadata(version int, document interface{ validate() error }) ([]byte, error) {
	if err := validateMetadataVersion(version); err != nil {
		return nil, err
	}
	if err := document.validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode dependency metadata: %w", err)
	}
	if len(data)+1 > maxMetadataBytes {
		return nil, errors.New("dependency metadata exceeds size limit")
	}
	return append(data, '\n'), nil
}

func decodeMetadata(reader io.Reader, document interface{ validate() error }) error {
	data, err := io.ReadAll(io.LimitReader(reader, maxMetadataBytes+1))
	if err != nil {
		return fmt.Errorf("read dependency metadata: %w", err)
	}
	if len(data) > maxMetadataBytes {
		return errors.New("dependency metadata exceeds size limit")
	}
	if err := inspectMetadata(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(document); err != nil {
		return fmt.Errorf("decode dependency metadata: %w", err)
	}
	return document.validate()
}

func inspectMetadata(data []byte) error {
	var header map[string]json.RawMessage
	if err := json.Unmarshal(data, &header); err != nil {
		return errors.New("dependency metadata must be one JSON object")
	}
	var version int
	if err := json.Unmarshal(header["schemaVersion"], &version); err != nil {
		return errors.New("metadata schemaVersion is required")
	}
	if err := validateMetadataVersion(version); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := checkJSONValue(decoder); err != nil {
		return fmt.Errorf("invalid dependency metadata: %w", err)
	}
	return metadataExactKeys(data)
}

func validateMetadataVersion(version int) error {
	if version > MetadataSchemaVersion {
		return &UnsupportedMetadataSchemaError{Version: version}
	}
	if version != MetadataSchemaVersion {
		return errors.New("invalid dependency metadata schema version")
	}
	return nil
}

// encoding/json accepts case aliases for field names. Restrict the metadata
// vocabulary so aliases cannot silently overwrite a previously decoded field.
func metadataExactKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		key, ok := metadataJSONKey(token, data[decoder.InputOffset():])
		if !ok {
			continue
		}
		switch key {
		case "schemaVersion", "identity", "recipeId", "recipeSha256", "sourceUrl", "installedAt",
			"layout", "components", "validation", "toolId", "runtimeVersion", "platform", "archiveSha256",
			"path", "type", "sha256", "size", "archive", "revision", "receipts", "operationId", "phase",
			"artifacts", "startedAt", "updatedAt", "terminal", "at", "reasonCode":
		default:
			return errors.New("unknown dependency metadata field")
		}
	}
}

func metadataID(value string) bool {
	if len(value) == 0 || len(value) > 64 || reservedMetadataID(value) {
		return false
	}
	return strings.IndexFunc(value, func(r rune) bool {
		return !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz0123456789-", r)
	}) == -1
}

func metadataTime(value time.Time) bool {
	return !value.IsZero() && value.Year() >= 1970 && value.Year() <= 9999
}

func reservedMetadataID(value string) bool {
	switch value {
	case "con",
		"prn",
		"aux",
		"nul",
		"com1",
		"com2",
		"com3",
		"com4",
		"com5",
		"com6",
		"com7",
		"com8",
		"com9",
		"lpt1",
		"lpt2",
		"lpt3",
		"lpt4",
		"lpt5",
		"lpt6",
		"lpt7",
		"lpt8",
		"lpt9":
		return true
	default:
		return false
	}
}

func metadataJSONKey(token json.Token, rest []byte) (string, bool) {
	key, ok := token.(string)
	if !ok {
		return "", false
	}
	rest = bytes.TrimSpace(rest)
	return key, len(rest) > 0 && rest[0] == ':'
}
