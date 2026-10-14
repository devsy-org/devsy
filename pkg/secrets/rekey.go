package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
// rolls back without needing either credential. Committed records the chosen
// terminal outcome (the new state or a completed rollback), so cleanup can
// safely remove backups before removing the journal.
func RecoverRekey(dir string) error {
	j, err := readRekeyJournal(dir)
	if errors.Is(err, os.ErrNotExist) {
		// A crash may happen before the journal is written, after backups or
		// secrets.enc.next were created. No active files have been swapped yet.
		return cleanupOrphanRekeyFiles(dir)
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
		if err = markRekeyFinalized(dir, j); err != nil {
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
	return cleanupRekeyFiles(dir, removeRekeyFile)
}

// cleanupRekeyFiles retains the finalized journal while deleting backups. If
// interrupted, recovery sees the terminal outcome and safely repeats cleanup.
func cleanupRekeyFiles(dir string, remove func(string) error) error {
	for _, name := range []string{rekeyBlobBackup, rekeyIndexBackup, rekeyNextName} {
		if err := remove(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	names, err := rekeyTempNames(dir)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := remove(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	if err := syncRekeyDir(dir); err != nil {
		return err
	}
	if err := remove(filepath.Join(dir, rekeyJournalName)); err != nil {
		return err
	}
	return syncRekeyDir(dir)
}

// No journal means any transaction stopped before swapping active files, or
// cleanup had already reached its journal-last final step. Known artifacts
// are therefore safe to remove while the caller holds the store lock.
func cleanupOrphanRekeyFiles(dir string) error {
	names, err := rekeyTempNames(dir)
	if err != nil {
		return err
	}
	changed := false
	for _, name := range append(names, rekeyBlobBackup, rekeyIndexBackup, rekeyNextName) {
		removed, err := removeRekeyFileIfPresent(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		changed = changed || removed
	}
	if changed {
		return syncRekeyDir(dir)
	}
	return nil
}

func removeRekeyFileIfPresent(path string) (bool, error) {
	// #nosec G703 -- only fixed artifact names or validated internal temp basenames under the store lock.
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Atomic writes can leave a temp file on process death, before their rename.
// The store lock excludes active writers for these reserved filename prefixes.
func rekeyTempNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		for _, target := range []string{
			rekeyBlobBackup, rekeyIndexBackup, rekeyNextName, rekeyJournalName,
			EncryptedFileName, IndexFileName,
		} {
			if strings.HasPrefix(entry.Name(), target+".tmp-") {
				names = append(names, entry.Name())
				break
			}
		}
	}
	return names, nil
}

func markRekeyFinalized(dir string, j *rekeyJournal) error {
	j.Committed = true
	return writeRekeyJournal(dir, *j)
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
	if err := atomicWriteFile(filepath.Join(dir, rekeyJournalName), raw, 0o600); err != nil {
		return err
	}
	return syncRekeyDir(dir)
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
