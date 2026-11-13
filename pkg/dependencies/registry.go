package dependencies

import "errors"

// Registry is an inventory snapshot. Revision is metadata for a later locked
// transaction; encoding or atomically replacing it does not perform CAS.
type Registry struct {
	SchemaVersion int               `json:"schemaVersion"`
	Revision      uint64            `json:"revision"`
	Receipts      []ReceiptIdentity `json:"receipts"`
}

func (r Registry) validate() error {
	if r.Revision == 0 || r.Receipts == nil || len(r.Receipts) > maxMetadataReferences {
		return errors.New("registry requires a positive revision and bounded receipt inventory")
	}
	return validateReceiptIdentities(r.Receipts)
}

func validateReceiptIdentities(identities []ReceiptIdentity) error {
	bundles := make(map[string]bool)
	for _, identity := range identities {
		if err := identity.validate(); err != nil {
			return err
		}
		key := identity.Platform + "/" + identity.ArchiveSHA256
		if bundles[key] {
			return errors.New("duplicate immutable receipt identity")
		}
		bundles[key] = true
	}
	return nil
}
