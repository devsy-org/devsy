package dependencies

import (
	"errors"
	"slices"
	"time"
)

type JournalPhase string

const (
	metadataPolicyBlocked              = "dependency_policy_blocked"
	PhasePlanned          JournalPhase = "planned"
	PhaseDownloading      JournalPhase = "downloading"
	PhaseVerifiedArchive  JournalPhase = "verified_archive"
	PhaseExtracting       JournalPhase = "extracting"
	PhasePublished        JournalPhase = "published"
	PhaseBinding          JournalPhase = "binding"
	PhaseComplete         JournalPhase = "complete"
	PhaseCanceled         JournalPhase = "canceled"
	PhaseFailed           JournalPhase = "failed"
	PhaseInterrupted      JournalPhase = "interrupted"
)

// Journal contains references and bounded outcome codes, never environment,
// endpoint tokens, raw diagnostic output, or a PID-based liveness assertion.
type Journal struct {
	SchemaVersion int               `json:"schemaVersion"`
	OperationID   string            `json:"operationId"`
	Phase         JournalPhase      `json:"phase"`
	Artifacts     []ReceiptIdentity `json:"artifacts"`
	Receipts      []ReceiptIdentity `json:"receipts"`
	StartedAt     time.Time         `json:"startedAt"`
	UpdatedAt     time.Time         `json:"updatedAt"`
	Terminal      *JournalTerminal  `json:"terminal,omitempty"`
}

type JournalTerminal struct {
	At         time.Time `json:"at"`
	ReasonCode string    `json:"reasonCode"`
}

func (j Journal) validate() error {
	if !metadataID(j.OperationID) || !validJournalPhase(j.Phase) {
		return errors.New("invalid operation identity or journal phase")
	}
	if !metadataTime(j.StartedAt) || !metadataTime(j.UpdatedAt) || j.UpdatedAt.Before(j.StartedAt) {
		return errors.New("invalid journal timestamps")
	}
	if err := j.validateReferences(); err != nil {
		return err
	}
	return j.validateTerminal()
}

func validJournalPhase(phase JournalPhase) bool {
	return slices.Contains([]JournalPhase{
		PhasePlanned, PhaseDownloading, PhaseVerifiedArchive, PhaseExtracting, PhasePublished,
		PhaseBinding, PhaseComplete, PhaseCanceled, PhaseFailed, PhaseInterrupted,
	}, phase)
}

func (j Journal) validateReferences() error {
	if !boundedJournalReferences(j.Artifacts, j.Receipts) {
		return errors.New("journal requires bounded artifact and receipt references")
	}
	if err := validateReceiptIdentities(j.Artifacts); err != nil {
		return err
	}
	if err := validateReceiptIdentities(j.Receipts); err != nil {
		return err
	}
	for _, receipt := range j.Receipts {
		if !slices.Contains(j.Artifacts, receipt) {
			return errors.New("journal receipt has no referenced artifact")
		}
	}
	return nil
}

func (j Journal) validateTerminal() error {
	terminal := slices.Contains(
		[]JournalPhase{PhaseComplete, PhaseCanceled, PhaseFailed, PhaseInterrupted},
		j.Phase,
	)
	if terminal != (j.Terminal != nil) {
		return errors.New("terminal data must match journal phase")
	}
	if !terminal {
		return nil
	}
	if !metadataTime(j.Terminal.At) || !j.Terminal.At.Equal(j.UpdatedAt) {
		return errors.New("terminal time must match journal update time")
	}
	allowed := map[JournalPhase][]string{
		PhaseComplete: {"complete"},
		PhaseCanceled: {"canceled"},
		PhaseFailed: {
			"dependency_integrity_failed",
			"dependency_store_unwritable",
			metadataPolicyBlocked,
		},
		PhaseInterrupted: {"dependency_operation_interrupted"},
	}
	if !slices.Contains(allowed[j.Phase], j.Terminal.ReasonCode) {
		return errors.New("invalid terminal reason code")
	}
	return nil
}

func boundedJournalReferences(artifacts, receipts []ReceiptIdentity) bool {
	return len(artifacts) > 0 && len(artifacts) <= maxMetadataReferences && receipts != nil &&
		len(receipts) <= maxMetadataReferences
}
