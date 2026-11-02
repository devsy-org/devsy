package ghcredentials

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

type overlayAccount struct {
	Host   string `json:"host"`
	User   string `json:"user"`
	Digest string `json:"digest"`
}

func fingerprint(a Account) overlayAccount {
	digest := sha256.Sum256([]byte(a.Host + "\x00" + a.User + "\x00" + a.Token))
	return overlayAccount{Host: a.Host, User: a.User, Digest: hex.EncodeToString(digest[:])}
}

func (a overlayAccount) matches(token string) bool {
	return fingerprint(Account{Host: a.Host, User: a.User, Token: token}).Digest == a.Digest
}

type publication struct {
	Name       string      `json:"name"`
	Target     string      `json:"target"`
	Preimage   string      `json:"preimage"`
	Postimage  string      `json:"postimage"`
	Result     []byte      `json:"result"`
	Mode       fs.FileMode `json:"mode"`
	Applied    bool        `json:"applied"`
	Publishing bool        `json:"publishing,omitempty"`
	Conflict   bool        `json:"conflict"`
}

func imageDigest(data []byte) string {
	prefix := byte(1)
	if data == nil {
		prefix = 0
	}
	h := sha256.New()
	_, _ = h.Write([]byte{prefix})
	_, _ = h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func (s *session) sanitize() error {
	if s.Sanitized {
		return nil
	}
	err := s.sanitizeWork()
	if err != nil && !errors.Is(err, errLostIntent) {
		return err
	}
	s.Accounts = nil
	s.Sanitized = true
	s.Version = 2
	if saveErr := s.save(); saveErr != nil {
		return errors.Join(
			boundary("session.sanitized_checkpoint", saveErr),
			boundary("session.metadata_cleanup", os.Remove(filepath.Join(s.dir, "session.json"))),
		)
	}
	if err != nil {
		return boundary("session.lost_intent", err)
	}
	return s.checkpointAt("sanitized")
}

var errLostIntent = errors.New("unreadable GitHub CLI working configuration discarded")

func (s *session) sanitizedHosts() ([]byte, error) {
	before, err := readFile(filepath.Join(s.dir, "before", hostsFile))
	if err != nil {
		return nil, err
	}
	data, err := readFile(filepath.Join(s.work(), hostsFile))
	if err != nil || len(data) == 0 {
		return data, err
	}
	return s.strip(before, data)
}

func (s *session) sanitizeWork() error {
	data, err := s.sanitizedHosts()
	path := filepath.Join(s.work(), hostsFile)
	if err == nil && data != nil {
		err = writeOriginal(path, data, 0o600)
	}
	if err == nil {
		return nil
	}
	if cleanupErr := removeFile(path); cleanupErr != nil {
		return boundary("session.scrub", cleanupErr)
	}
	s.LostHosts = true
	return errLostIntent
}

func removeFile(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *session) checkpointAt(stage string) error {
	if s.checkpoint != nil {
		return s.checkpoint(stage)
	}
	return nil
}

func (s *session) planPublication(name string, latest, result []byte, conflict bool) error {
	target, err := s.originalPath(name)
	if err != nil {
		return err
	}
	mode := fs.FileMode(0o600)
	if info, statErr := os.Stat(
		filepath.Join(s.work(), name),
	); statErr == nil &&
		info.Mode()&0o111 != 0 {
		mode = 0o700
	}
	s.Publications = append(
		s.Publications,
		publication{
			Name:      name,
			Target:    target,
			Preimage:  imageDigest(latest),
			Postimage: imageDigest(result),
			Result:    result,
			Mode:      mode,
			Conflict:  conflict,
		},
	)
	if err := s.save(); err != nil {
		s.Publications = s.Publications[:len(s.Publications)-1]
		return boundary("config.plan_checkpoint", err)
	}
	if err := s.checkpointAt("planned:" + name); err != nil {
		return err
	}
	return s.publish(&s.Publications[len(s.Publications)-1])
}

func (s *session) publish(p *publication) error {
	if p.Applied {
		if p.Conflict {
			return errAuthConflict
		}
		return nil
	}
	if !validPublication(p) {
		return errInvalidConfig
	}
	if err := s.applyPublication(p); err != nil {
		return err
	}
	return s.markApplied(p)
}

func (s *session) applyPublication(p *publication) error {
	target, err := s.originalPath(p.Name)
	if err != nil {
		return err
	}
	current, err := readFile(target)
	if err != nil {
		return boundary("config.latest_read", err)
	}
	switch {
	case target != p.Target:
		p.Conflict = true
	case imageDigest(current) == p.Postimage:
	case imageDigest(current) != p.Preimage:
		p.Conflict = true
	case p.Publishing:
		p.Conflict = true
	default:
		return s.writePublication(p)
	}
	return nil
}

func (s *session) writePublication(p *publication) error {
	p.Publishing = true
	if err := s.save(); err != nil {
		p.Publishing = false
		return boundary("config.publishing_checkpoint", err)
	}
	if err := s.checkpointAt("publishing:" + p.Name); err != nil {
		return err
	}
	needsWrite, err := s.publicationNeedsWrite(p)
	if err != nil || !needsWrite {
		return err
	}
	if err := writePublication(p); err != nil {
		p.Publishing = false
		if saveErr := s.save(); saveErr != nil {
			return errors.Join(
				boundary("config.write", err),
				boundary("config.retry_checkpoint", saveErr),
			)
		}
		return boundary("config.write", err)
	}
	if err := s.checkpointAt("published:" + p.Name); err != nil {
		return err
	}
	return nil
}

func (s *session) publicationNeedsWrite(p *publication) (bool, error) {
	target, err := s.originalPath(p.Name)
	if err != nil {
		return false, err
	}
	if target != p.Target {
		return false, errAuthConflict
	}
	current, err := readFile(target)
	if err != nil {
		return false, boundary("config.latest_read", err)
	}
	switch imageDigest(current) {
	case p.Postimage:
		return false, nil
	case p.Preimage:
		return true, nil
	default:
		p.Conflict = true
		return false, nil
	}
}

func (s *session) markApplied(p *publication) error {
	p.Applied = true
	if err := s.save(); err != nil {
		p.Applied = false
		return boundary("config.applied_checkpoint", err)
	}
	if err := s.checkpointAt("applied:" + p.Name); err != nil {
		return err
	}
	if p.Conflict {
		return errAuthConflict
	}
	return nil
}

func writePublication(p *publication) error {
	if p.Result == nil {
		return removeFile(p.Target)
	}
	return writeResolvedOriginal(p.Target, p.Result, p.Mode)
}

func validPublication(p *publication) bool {
	return p.Name != "" && !filepath.IsAbs(p.Name) && filepath.IsLocal(p.Name) &&
		filepath.IsAbs(p.Target) &&
		len(p.Preimage) == 64 &&
		p.Postimage == imageDigest(p.Result) &&
		(p.Mode == 0o600 || p.Mode == 0o700)
}

func (s *session) completed() bool {
	for _, p := range s.Publications {
		if !p.Applied {
			return false
		}
	}
	return true
}

func onlyAuthConflict(err error) bool {
	if err == errAuthConflict {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if !onlyAuthConflict(child) {
				return false
			}
		}
		return true
	}
	return false
}

func (a overlayAccount) valid() bool { return a.Host != "" && a.User != "" && len(a.Digest) == 64 }
