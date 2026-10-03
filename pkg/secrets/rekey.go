package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const (
	rekeyJournalName = "secrets.rekey.json"
	rekeyBlobBackup  = "secrets.enc.rekey-backup"
	rekeyIndexBackup = "secrets.yaml.rekey-backup"
	rekeyNextName    = "secrets.enc.next"
)

type rekeyJournal struct {
	Version    int          `json:"version"`
	OldSource  string       `json:"oldSource"`
	NewSource  string       `json:"newSource"`
	HadBlob    bool         `json:"hadBlob"`
	HadIndex   bool         `json:"hadIndex"`
	Committed  bool         `json:"committed"`
	Reset      bool         `json:"reset,omitempty"`
	Quarantine string       `json:"quarantine,omitempty"`
	Entries    []SecretMeta `json:"entries,omitempty"`
}

// RecoverRekey must run under secrets.yaml.lock. An uncommitted transaction
// rolls back without needing either credential. Verified transactions finish
// cleanup. Backups remain until the journal has been removed durably.
func RecoverRekey(dir string) error {
	j, err := readRekeyJournal(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if j.Reset {
		return recoverReset(dir, *j)
	}
	if !j.Committed {
		if err = rollbackRekey(dir, *j); err != nil {
			return err
		}
	}
	return cleanupRekey(dir)
}

func readRekeyJournal(dir string) (*rekeyJournal, error) {
	// #nosec G304 G703 -- fixed journal filename within the selected config directory.
	raw, err := os.ReadFile(
		filepath.Join(dir, rekeyJournalName),
	)
	if err != nil {
		return nil, err
	}
	var j rekeyJournal
	if err = json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("invalid rekey journal: %w", err)
	}
	if err = validateRekeyJournal(j); err != nil {
		return nil, err
	}
	return &j, nil
}

func validRekeySource(source string) bool {
	switch keySource(source) {
	case keySourcePassphrase, keySourceAutoFile, keySourceKeyring:
		return true
	default:
		return false
	}
}

func rollbackRekey(dir string, j rekeyJournal) error {
	if err := restoreRekeyFile(dir, rekeyBlobBackup, EncryptedFileName, j.HadBlob); err != nil {
		return err
	}
	// Only protection metadata changes during rekey; preserve unrelated env
	// migration or catalog changes since interruption.
	idx, err := loadIndex(filepath.Join(dir, IndexFileName))
	if err != nil {
		return err
	}
	idx.data.KeySource = j.OldSource
	return idx.save()
}

func restoreRekeyFile(dir, backup, target string, existed bool) error {
	if !existed {
		return removeRekeyFile(filepath.Join(dir, target))
	}
	// #nosec G304 G703 -- backup and target are fixed internal filenames within the config directory.
	raw, err := os.ReadFile(
		filepath.Join(dir, backup),
	)
	if err != nil {
		return fmt.Errorf("read rekey rollback backup: %w", err)
	}
	return atomicWriteFile(filepath.Join(dir, target), raw, 0o600)
}

func removeRekeyFile(path string) error {
	// #nosec G703 -- callers supply only fixed rekey filenames within the config directory.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func cleanupRekey(dir string) error {
	// Journal first: a crash during backup cleanup must not request rollback
	// using backups that have already been deleted.
	if err := removeRekeyFile(filepath.Join(dir, rekeyJournalName)); err != nil {
		return err
	}
	if err := syncRekeyDir(dir); err != nil {
		return err
	}
	for _, name := range []string{rekeyBlobBackup, rekeyIndexBackup, rekeyNextName} {
		if err := removeRekeyFile(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return syncRekeyDir(dir)
}

func syncRekeyDir(dir string) error {
	// #nosec G304 G703 -- explicitly selected config directory, opened only for fsync.
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	// Directory fsync is not supported by every platform; atomicWriteFile
	// supplies the platform-specific persistence guarantees for file contents.
	if err = f.Sync(); err != nil && runtime.GOOS != "windows" && !errors.Is(err, os.ErrInvalid) {
		return err
	}
	return nil
}

func writeRekeyJournal(dir string, j rekeyJournal) error {
	raw, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return atomicWriteFile(filepath.Join(dir, rekeyJournalName), raw, 0o600)
}

func backupRekeyFile(dir, target, backup string) (bool, error) {
	// #nosec G304 G703 -- target is a fixed internal store filename within the config directory.
	raw, err := os.ReadFile(
		filepath.Join(dir, target),
	)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, atomicWriteFile(filepath.Join(dir, backup), raw, 0o600)
}

func validateRekeyJournal(j rekeyJournal) error {
	if j.Version != 1 {
		return errors.New("unsupported rekey journal version")
	}
	if j.OldSource != "" && !validRekeySource(j.OldSource) {
		return errors.New("invalid rekey journal previous protection source")
	}
	if j.Reset {
		return validateResetJournal(j)
	}
	if !validRekeySource(j.NewSource) {
		return errors.New("invalid rekey journal protection source")
	}
	return nil
}
