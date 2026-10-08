package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"github.com/stretchr/testify/require"
)

// Functional fixtures use real age/scrypt at a cost unsuitable for user secrets.
// Production defaults are verified separately by the crypto_integration test.
const testScryptWorkFactor = 10

func testPassphraseFileKey(t *testing.T, passphrase string) *fileKey {
	t.Helper()
	recipient, err := age.NewScryptRecipient(passphrase)
	require.NoError(t, err)
	recipient.SetWorkFactor(testScryptWorkFactor)
	identity, err := age.NewScryptIdentity(passphrase)
	require.NoError(t, err)
	return &fileKey{recipient: recipient, identity: identity, source: keySourcePassphrase}
}

func useFastPassphraseTargets(t *testing.T, p *ProtectionManager) {
	t.Helper()
	p.resolveNewFileKey = func(dir, passphrase string) (*fileKey, error) {
		if passphrase == "" {
			return resolveFileKey(dir, passphrase)
		}
		return testPassphraseFileKey(t, passphrase), nil
	}
}

func TestPassphraseFixtureWorkFactor(t *testing.T) {
	t.Setenv(EnvPassphrase, "fixture passphrase")
	path := filepath.Join(t.TempDir(), EncryptedFileName)
	backend := newPassphraseBackend(t, path, "fixture passphrase")
	require.NoError(t, backend.set("default/FIXTURE", "fixture value"))
	blob, err := os.ReadFile(path) // #nosec G304 -- isolated test directory.
	require.NoError(t, err)
	assertScryptHeader(t, blob, "10")

	key, err := openPassphraseFileKey()
	require.NoError(t, err)
	value, err := newFileBackend(path, key).get("default/FIXTURE")
	require.NoError(t, err)
	require.Equal(t, "fixture value", value)
}

type scryptHeaderInspector struct {
	stanzas []*age.Stanza
}

func (i *scryptHeaderInspector) Unwrap(stanzas []*age.Stanza) ([]byte, error) {
	i.stanzas = stanzas
	return nil, age.ErrIncorrectIdentity
}

func assertScryptHeader(t *testing.T, blob []byte, workFactor string) {
	t.Helper()
	header, err := age.ExtractHeader(bytes.NewReader(blob))
	require.NoError(t, err)
	// Public Identity callbacks expose parsed stanzas without deriving a key.
	inspector := &scryptHeaderInspector{}
	_, err = age.DecryptHeader(header, inspector)
	var noMatch *age.NoIdentityMatchError
	require.ErrorAs(t, err, &noMatch)
	require.Len(t, inspector.stanzas, 1)
	stanza := inspector.stanzas[0]
	require.Equal(t, "scrypt", stanza.Type)
	require.Len(t, stanza.Args, 2)
	require.Equal(t, workFactor, stanza.Args[1])
}
