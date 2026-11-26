package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (p *ProtectionManager) resetFileStore(expected []SecretMeta) (string, error) {
	unlock, err := p.lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	idx, err := loadIndex(filepath.Join(p.dir, IndexFileName))
	if err != nil {
		return "", err
	}
	tx, err := p.prepareReset(idx, expected)
	if err != nil {
		return "", err
	}
	if err = tx.reset(); err != nil {
		return "", err
	}
	if !tx.journal.HadBlob {
		return "", nil
	}
	return filepath.Join(p.dir, tx.journal.Quarantine), nil
}

func (p *ProtectionManager) prepareReset(
	idx *index,
	expected []SecretMeta,
) (*protectionTransaction, error) {
	if err := p.ensureKnownFileOwnership(idx); err != nil {
		return nil, err
	}
	entries := fileEntries(idx)
	if expected != nil && !sameFileEntries(entries, expected) {
		return nil, errors.New(
			"file-backed secret list changed; review names and confirm reset again",
		)
	}
	// #nosec G703 -- fixed ciphertext filename within the selected config directory.
	_, err := os.Stat(filepath.Join(p.dir, EncryptedFileName))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return &protectionTransaction{manager: p, idx: idx, journal: rekeyJournal{
		Version:    1,
		Reset:      true,
		HadBlob:    err == nil,
		OldSource:  idx.data.KeySource,
		Entries:    entries,
		Quarantine: fmt.Sprintf("secrets.enc.quarantine-%d", time.Now().UnixNano()),
	}}, nil
}

func (tx *protectionTransaction) reset() error {
	return tx.runSteps([]protectionStep{
		{"reset-journal", func() error { return writeRekeyJournal(tx.manager.dir, tx.journal) }},
		{"reset-quarantined", tx.quarantine},
		{"reset-metadata", tx.saveResetMetadata},
		{"reset-committed", tx.commit},
		{"reset-cleanup", func() error { return cleanupRekey(tx.manager.dir) }},
	})
}

func (tx *protectionTransaction) quarantine() error {
	dir := tx.manager.dir
	if tx.journal.HadBlob {
		// #nosec G703 -- transaction generates the quarantine basename; both paths stay in the config directory.
		if err := os.Rename(
			filepath.Join(dir, EncryptedFileName),
			filepath.Join(dir, tx.journal.Quarantine),
		); err != nil {
			return err
		}
	}
	return syncRekeyDir(dir)
}

func (tx *protectionTransaction) saveResetMetadata() error {
	for _, meta := range tx.journal.Entries {
		tx.idx.remove(meta.Context, meta.Name)
	}
	tx.idx.data.KeySource = ""
	return tx.idx.save()
}

func recoverReset(dir string, j rekeyJournal) error {
	if err := validateResetJournal(j); err != nil {
		return err
	}
	if !j.Committed {
		if err := restoreResetBlob(dir, j); err != nil {
			return err
		}
		if err := restoreResetMetadata(dir, j); err != nil {
			return err
		}
		if err := markRekeyFinalized(dir, &j); err != nil {
			return err
		}
	}
	return cleanupRekey(dir)
}

func restoreResetBlob(dir string, j rekeyJournal) error {
	if err := validateResetJournal(j); err != nil {
		return err
	}
	if !j.HadBlob {
		return nil
	}
	quarantine := filepath.Join(dir, j.Quarantine)
	_, err := os.Stat(
		quarantine,
	) // #nosec G703 -- journal basename validated above; config directory only.
	if errors.Is(err, os.ErrNotExist) {
		// #nosec G703 -- fixed ciphertext filename in the selected config directory.
		if _, err = os.Stat(
			filepath.Join(dir, EncryptedFileName),
		); err != nil {
			return errors.New("reset rollback ciphertext is missing")
		}
		return nil
	}
	if err != nil {
		return err
	}
	// #nosec G703 -- quarantine basename validated above; fixed config target.
	if err = os.Rename(
		quarantine,
		filepath.Join(dir, EncryptedFileName),
	); err != nil {
		return err
	}
	return syncRekeyDir(dir)
}

func restoreResetMetadata(dir string, j rekeyJournal) error {
	idx, err := loadIndex(filepath.Join(dir, IndexFileName))
	if err != nil {
		return err
	}
	idx.data.KeySource = j.OldSource
	for _, meta := range j.Entries {
		idx.put(meta)
	}
	return idx.save()
}

func validateResetJournal(j rekeyJournal) error {
	if filepath.Base(j.Quarantine) != j.Quarantine ||
		!strings.HasPrefix(j.Quarantine, "secrets.enc.quarantine-") {
		return errors.New("invalid reset journal quarantine name")
	}
	for _, meta := range j.Entries {
		if meta.Backend != BackendFile || !meta.Sensitive() || meta.Value != "" {
			return errors.New("invalid reset journal catalog entry")
		}
		if err := ValidateName(meta.Name); err != nil {
			return errors.New("invalid reset journal secret name")
		}
	}
	return nil
}

// A legacy unowned secret may reside inside the indivisible encrypted blob.
// Never omit it from confirmation or assume that it belongs to the keyring.
func (p *ProtectionManager) ensureKnownFileOwnership(idx *index) error {
	if !hasUnownedSecrets(idx) {
		return nil
	}
	// #nosec G703 -- fixed ciphertext filename in the selected config directory.
	_, err := os.Stat(filepath.Join(p.dir, EncryptedFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf(
		"cannot identify all file-backed secrets while ownership remains unknown; "+
			"restore backend access and repair secret ownership before resetting: %w",
		ErrStateIndeterminate,
	)
}

func hasUnownedSecrets(idx *index) bool {
	for _, entries := range idx.data.Contexts {
		for _, meta := range entries {
			if meta.Sensitive() && meta.Backend == "" {
				return true
			}
		}
	}
	return false
}
