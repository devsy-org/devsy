package dependencies

import "errors"

type LookupOrigin string

const (
	LookupExplicit           LookupOrigin = "explicit_option"
	LookupExplicitEnv        LookupOrigin = "explicit_env"
	LookupPATH               LookupOrigin = "external_path"
	LookupReviewed           LookupOrigin = "well_known_path"
	LookupMissing                         = "dependency_missing"
	LookupInaccessible                    = "dependency_inaccessible"
	LookupInvalidPath                     = "dependency_path_invalid"
	LookupIndirectLauncher                = "dependency_indirect_launcher"
	LookupInspectionDeferred              = "dependency_inspection_deferred"
)

// LookupRequest contains a command name or an explicit invocation path.
// Explicit retains user intent even for an empty value or a manifest default.
// ReviewedLocations are ordered, complete absolute executable paths reviewed by
// the caller for this provider/host. They are not directories to scan.
type LookupRequest struct {
	Command  string
	Explicit bool
	// ExplicitOrigin defaults to explicit_option and may also be explicit_env.
	ExplicitOrigin    LookupOrigin
	ReviewedLocations []string
}

// LookupResult is filesystem evidence, not a selection or a full Survey report.
// A deferred candidate retains lexical evidence without an executable claim.
// An empty ReasonCode means at least one executable file was observed; it says
// nothing about version, compatibility, reachability, native format, or trust.
type LookupResult struct {
	Candidates []ExecutableCandidate `json:"candidates"`
	ReasonCode string                `json:"reasonCode,omitempty"`
	Executed   bool                  `json:"executed"`
	// keys holds each appended candidate's comparison key while a result is built.
	keys map[string]struct{}
}

// append drops missing candidates and repeats of one path. Repeats are judged
// the way the platform compares names, so C:\Tools\a.exe and c:\tools\A.EXE match.
// Each key is computed once, so the cost grows linearly with the candidate count.
func (r *LookupResult) append(environment LookupEnvironment, candidate ExecutableCandidate) {
	if candidate.State == FileMissing {
		return
	}
	key := environment.lexicalKey(candidate.LexicalPath)
	if _, repeated := r.keys[key]; repeated {
		return
	}
	if r.keys == nil {
		r.keys = make(map[string]struct{})
	}
	r.keys[key] = struct{}{}
	r.Candidates = append(r.Candidates, candidate)
}

func (r *LookupResult) finish(explicit bool) {
	r.keys = nil
	r.ReasonCode = LookupMissing
	if explicit {
		r.ReasonCode = LookupInvalidPath
	}
	for _, candidate := range r.Candidates {
		if candidate.State == FileExecutable || candidate.State == FileDeferred {
			r.ReasonCode = candidate.ReasonCode
			return
		}
	}
	for _, candidate := range r.Candidates {
		if candidate.State == FileInaccessible {
			r.ReasonCode = candidate.ReasonCode
			return
		}
	}
}

func (e LookupEnvironment) validateAutomaticLookup(request LookupRequest) error {
	if request.Explicit || request.ExplicitOrigin != "" {
		return errors.New("automatic lookup cannot be explicit")
	}
	if !commandName(request.Command, e.windows) {
		return errors.New("automatic lookup requires a command name")
	}
	for _, path := range request.ReviewedLocations {
		if !e.localAbsolute(path) {
			return errors.New("reviewed executable location must be a local absolute path")
		}
	}
	return nil
}
