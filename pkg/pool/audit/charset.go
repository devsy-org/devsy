package audit

import (
	"regexp"
	"strings"
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

	sessionIDPrefix    = "p-"
	sessionIDSuffixLen = 10
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

// sessionPattern is the shape session.NewID returns: p-<pool>-<10 base32
// characters>, with a pool name of at most 32 characters.
var sessionPattern = regexp.MustCompile(`^p-[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?-[a-z2-7]{10}$`)

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

// checkSession accepts an empty session or an ID that belongs to pool. The
// exact length keeps pool a from accepting an ID of pool a-b.
func checkSession(e Event, pool string) error {
	if e.Session == "" {
		return nil
	}
	prefix := sessionIDPrefix + pool + "-"
	if sessionPattern.MatchString(e.Session) &&
		strings.HasPrefix(e.Session, prefix) &&
		len(e.Session) == len(prefix)+sessionIDSuffixLen {
		return nil
	}
	return refuse("write audit event %s: session is not a session ID of this pool", e.Type)
}

func validLocalUser(v string) bool {
	return len(v) <= maxLocalUserBytes && localUserPattern.MatchString(v)
}

func charsetError(typ, key string) error {
	return refuse("write audit event %s: %s has characters outside its allowed set", typ, key)
}
