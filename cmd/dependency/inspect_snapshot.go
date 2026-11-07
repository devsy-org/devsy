package dependency

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
)

type providerSnapshotV1 struct {
	Version        int                           `json:"version"`
	Manifest       provider.ProviderConfig       `json:"manifest"`
	StoredOptions  map[string]config.OptionValue `json:"storedOptions"`
	DynamicOptions config.OptionDefinitions      `json:"dynamicOptions"`
}

// snapshotRevision fingerprints deterministic Go JSON, not RFC 8785 JSON.
// JSON-visible metadata, slice order and nil/empty follow encoding/json; json:"-"
// fields (origin and transient overrides) are excluded. These local fingerprints
// are not atomic cross-file snapshots, CAS authority, file identity or redaction.
func snapshotRevision(snapshot any) (string, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func inspectionEnvironment(captured []string) map[string]string {
	environment := make(map[string]string, len(captured))
	for _, entry := range captured {
		key, value, found := strings.Cut(entry, "=")
		if found {
			environment[key] = value
		}
	}
	// Preserve differently cased Windows keys for lookup's ambiguity validation.
	return environment
}
