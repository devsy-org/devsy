package framework

import (
	"encoding/json"
	"os"
	"sync"

	"github.com/devsy-org/devsy/e2e/framework/runenv"
)

var registeredSecrets = struct {
	sync.Mutex
	values map[string]struct{}
}{values: make(map[string]struct{})}

// RegisterSecret masks value in subsequent command diagnostics. Empty values
// are ignored. Values must be single-line: a value spanning a plain line and a
// JSON record may not be masked. Very short values can mask unrelated output.
// Raw Gomega failure messages can print expected literals; the sentinel check detects them.
func RegisterSecret(value string) {
	if value == "" {
		return
	}
	registeredSecrets.Lock()
	defer registeredSecrets.Unlock()
	if _, ok := registeredSecrets.values[value]; ok {
		return
	}
	if path := os.Getenv("DEVSY_E2E_SENTINEL_FILE"); path != "" {
		if err := appendSentinel(path, value); err != nil {
			panic("cannot write e2e sentinel file")
		}
	}
	registeredSecrets.values[value] = struct{}{}
}

func appendSentinel(path, value string) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return (runenv.OsFileStore{}).AppendFile(path, data, 0o600)
}

func registeredSecretValues() []string {
	registeredSecrets.Lock()
	defer registeredSecrets.Unlock()
	values := make([]string, 0, len(registeredSecrets.values))
	for value := range registeredSecrets.values {
		values = append(values, value)
	}
	return values
}
