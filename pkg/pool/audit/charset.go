package audit

import (
	"fmt"
	"regexp"
)

const (
	keyAgentName     = "devsy.agent.name"
	keyProviderName  = "devsy.provider.name"
	keyRepository    = "vcs.repository.name"
	keyExecutable    = "process.executable.name"
	keyUserID        = "user.id"
	keyRequesterKind = "devsy.requester.kind"

	requesterLocalUser = "local_user"
	maxLocalUserBytes  = 64
)

type charsetRule struct {
	key string
	re  *regexp.Regexp
}

// charsetRules limit free-string values to characters a name can hold, so a
// token or a sentence cannot ride in a field that only needs a name.
var charsetRules = []charsetRule{
	{keyAgentName, regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)},
	{keyProviderName, regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)},
	{keyRepository, regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}/[A-Za-z0-9._-]{1,100}$`)},
	{keyExecutable, regexp.MustCompile(`^[A-Za-z0-9._+-]{1,128}$`)},
}

// localUserPattern is an OS login name with an optional Windows domain.
// It has no @, so an email cannot pass.
var localUserPattern = regexp.MustCompile(
	`^([A-Za-z0-9._-]+\\)?[A-Za-z0-9_.][A-Za-z0-9._$-]*$`,
)

// checkCharsets rejects a string value outside its character set. Values of
// another type are left to the envelope validator. Errors name the key only.
func checkCharsets(e Event) error {
	for _, r := range charsetRules {
		if v, ok := e.Data[r.key].(string); ok && !r.re.MatchString(v) {
			return charsetError(e.Type, r.key)
		}
	}
	if kind, _ := e.Data[keyRequesterKind].(string); kind != requesterLocalUser {
		return nil
	}
	if v, ok := e.Data[keyUserID].(string); ok && !validLocalUser(v) {
		return charsetError(e.Type, keyUserID)
	}
	return nil
}

func validLocalUser(v string) bool {
	return len(v) <= maxLocalUserBytes && localUserPattern.MatchString(v)
}

func charsetError(typ, key string) error {
	return fmt.Errorf("write audit event %s: %s has characters outside its allowed set", typ, key)
}
