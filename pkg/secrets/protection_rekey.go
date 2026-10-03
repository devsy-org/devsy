package secrets

import (
	"errors"
	"os"
	"path/filepath"
)

type protectionTransaction struct {
	manager *ProtectionManager
	idx     *index
	target  *fileKey
	values  map[string]string
	journal rekeyJournal
}

type protectionStep struct {
	phase string
	run   func() error
}

func (p *ProtectionManager) rekey(passphrase string, requirePassphrase, automatic bool) error {
	if !automatic && passphrase == "" {
		return errors.New("passphrase must not be empty")
	}
	unlock, err := p.lock()
	if err != nil {
		return err
	}
	defer unlock()
	idx, err := loadIndex(filepath.Join(p.dir, IndexFileName))
	if err != nil {
		return err
	}
	if err = validateProtectionChange(idx, requirePassphrase); err != nil {
		return err
	}
	values, err := p.currentValues(idx)
	if err != nil {
		return err
	}
	target, err := resolveFileKey(p.dir, passphrase)
	if err != nil {
		return err
	}
	tx := protectionTransaction{
		manager: p,
		idx:     idx,
		target:  target,
		values:  values,
		journal: rekeyJournal{
			Version:   1,
			OldSource: idx.data.KeySource,
			NewSource: string(target.source),
		},
	}
	return tx.rekey()
}

func validateProtectionChange(idx *index, requirePassphrase bool) error {
	protected := idx.data.KeySource == string(keySourcePassphrase)
	if requirePassphrase && !protected {
		return errors.New("file store is not passphrase protected")
	}
	if !requirePassphrase && protected {
		return errors.New("file store is already passphrase protected; use change-passphrase")
	}
	return nil
}

func (p *ProtectionManager) currentValues(idx *index) (map[string]string, error) {
	if idx.data.KeySource != "" {
		if err := requireEncryptedFile(p.dir); err != nil {
			return nil, err
		}
		current, err := openExistingFileKeyWithResolver(p.dir, idx, p.resolver)
		if err != nil {
			return nil, err
		}
		return newFileBackend(filepath.Join(p.dir, EncryptedFileName), current).load()
	}
	// #nosec G703 -- fixed ciphertext filename within the selected config directory.
	if _, err := os.Stat(filepath.Join(p.dir, EncryptedFileName)); err == nil {
		return nil, errors.New("existing encrypted file has no protection metadata")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return map[string]string{}, nil
}

func (tx *protectionTransaction) rekey() error {
	return tx.runSteps([]protectionStep{
		{"encrypted", tx.encrypt},
		{"journal", tx.backupAndJournal},
		{"swapped", tx.swapBlob},
		{"metadata", tx.saveProtectionMetadata},
		{"verified", tx.verify},
		{"committed", tx.commit},
		{"cleanup", func() error { return cleanupRekey(tx.manager.dir) }},
	})
}

func (tx *protectionTransaction) runSteps(steps []protectionStep) error {
	for _, step := range steps {
		if err := step.run(); err != nil {
			return err
		}
		if err := tx.manager.checkpoint(step.phase); err != nil {
			return err
		}
	}
	return nil
}

func (tx *protectionTransaction) encrypt() error {
	return newFileBackend(filepath.Join(tx.manager.dir, rekeyNextName), tx.target).store(tx.values)
}

func (tx *protectionTransaction) backupAndJournal() error {
	dir := tx.manager.dir
	var err error
	tx.journal.HadBlob, err = backupRekeyFile(dir, EncryptedFileName, rekeyBlobBackup)
	if err != nil {
		return err
	}
	tx.journal.HadIndex, err = backupRekeyFile(dir, IndexFileName, rekeyIndexBackup)
	if err != nil {
		return err
	}
	return writeRekeyJournal(dir, tx.journal)
}

func (tx *protectionTransaction) swapBlob() error {
	dir := tx.manager.dir
	// #nosec G703 -- fixed internal ciphertext filenames within the selected config directory.
	if err := os.Rename(
		filepath.Join(dir, rekeyNextName),
		filepath.Join(dir, EncryptedFileName),
	); err != nil {
		return err
	}
	return syncRekeyDir(dir)
}

func (tx *protectionTransaction) saveProtectionMetadata() error {
	tx.idx.data.KeySource = string(tx.target.source)
	return tx.idx.save()
}

func (tx *protectionTransaction) verify() error {
	_, err := newFileBackend(filepath.Join(tx.manager.dir, EncryptedFileName), tx.target).load()
	return err
}

func (tx *protectionTransaction) commit() error {
	// Clear stale remembered unlock material before the durable commit. A
	// rollback may require externally supplied unlock input afterwards.
	if err := tx.manager.forgetExistingRemembered(); err != nil {
		return err
	}
	tx.journal.Committed = true
	return writeRekeyJournal(tx.manager.dir, tx.journal)
}

func requireEncryptedFile(dir string) error {
	// #nosec G703 -- fixed ciphertext filename within the selected config directory.
	_, err := os.Stat(filepath.Join(dir, EncryptedFileName))
	if errors.Is(err, os.ErrNotExist) {
		return ErrSecretNotFound
	}
	return err
}
