package secrets

import (
	"sort"
	"strings"
)

const redactMask = "***"

type Redactor struct {
	replacer *strings.Replacer
}

// NewRedactor masks the values (not keys) of KEY=VALUE entries; empty values are ignored.
func NewRedactor(secretsEnv []string) *Redactor {
	values := make([]string, 0, len(secretsEnv))
	for _, entry := range secretsEnv {
		_, value, ok := strings.Cut(entry, "=")
		if !ok || value == "" {
			continue
		}
		values = append(values, value)
	}
	if len(values) == 0 {
		return &Redactor{}
	}

	// Mask longer values first so an overlapping prefix (e.g. "sec" of "secret")
	// cannot partially match ahead of the full value.
	sort.SliceStable(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	pairs := make([]string, 0, len(values)*2)
	for _, v := range values {
		pairs = append(pairs, v, redactMask)
	}

	return &Redactor{replacer: strings.NewReplacer(pairs...)}
}

func (r *Redactor) Redact(s string) string {
	if r == nil || r.replacer == nil {
		return s
	}

	return r.replacer.Replace(s)
}
