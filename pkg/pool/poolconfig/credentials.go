package poolconfig

import (
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/secrets"
	"golang.org/x/net/http/httpguts"
)

const (
	ModeMint   = "mint"
	ModeInject = "inject"

	DeliverGitHelper = "git-helper"
	DeliverGHEnv     = "gh-env"

	defaultMintTTL = time.Hour
	// GitHub installation tokens expire after one hour and cannot be extended.
	maxMintTTL = time.Hour
	// GitHub refuses an installation token request naming more repositories.
	maxMintRepositories = 500

	malformedPlaceholder = "malformed placeholder; use {{route.<name>}}"

	levelRead  = "read"
	levelWrite = "write"
)

// Credential is a grant the broker serves to pool sessions. Mint grants name
// their issuer with one issuer block, today only GitHubApp; inject grants use
// Secret and Route. A zero mint TTL takes the default.
type Credential struct {
	Name      string     `json:"name"`
	Mode      string     `json:"mode"`
	GitHubApp *GitHubApp `json:"githubApp,omitempty"`
	TTL       Duration   `json:"ttl,omitzero"`
	Deliver   []string   `json:"deliver,omitempty"`
	Secret    string     `json:"secret,omitempty"`
	Route     *Route     `json:"route,omitempty"`
}

// GitHubApp mints installation tokens for one GitHub App installation.
type GitHubApp struct {
	AppID          string     `json:"appId"`
	InstallationID string     `json:"installationId"`
	PrivateKey     string     `json:"privateKey"`
	Scope          *MintScope `json:"scope"`
}

type MintScope struct {
	Repositories []string          `json:"repositories"`
	Permissions  map[string]string `json:"permissions"`
}

// Route is an inject endpoint on the egress sidecar that forwards to a fixed
// upstream and adds the credential as a header.
type Route struct {
	Name     string `json:"name"`
	Upstream string `json:"upstream"`
	Header   string `json:"header"`
	Prefix   string `json:"prefix,omitempty"`
}

var (
	placeholderRegex  = regexp.MustCompile(`\{\{\s*([^{}]*?)\s*\}\}`)
	strayRouteRegex   = regexp.MustCompile(`\{\s*route\.`)
	digitsPattern     = regexp.MustCompile(`^[0-9]+$`)
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	prefixPattern     = regexp.MustCompile(`^[\x20-\x7e]{0,64}$`)
	// permissionLevels maps each repository permission a pool agent may hold
	// to the highest level allowed. Everything else is refused, including
	// every organization permission and administration, secrets, workflows
	// and hooks, so a new GitHub permission stays refused until it is
	// reviewed. actions and environments are read only: write can dispatch
	// workflows that hold secrets or replace environment secrets.
	// attestations is read only: write can forge build provenance.
	permissionLevels = map[string]string{
		"actions": levelRead, "attestations": levelRead, "checks": levelWrite,
		"contents": levelWrite, "deployments": levelWrite, "discussions": levelWrite,
		"environments": levelRead, "issues": levelWrite, "merge_queues": levelWrite,
		"metadata": levelRead, "packages": levelWrite, "pages": levelWrite,
		"pull_requests": levelWrite, "repository_projects": levelWrite, "statuses": levelWrite,
	}
	allowedPermissions = slices.Sorted(maps.Keys(permissionLevels))
	secretRefTypes     = []string{secrets.LocalSourceName, secrets.SOPSFormatter}
	reservedHeaders    = []string{
		"host", "content-length", "transfer-encoding", "connection", "keep-alive", "te",
		"trailer", "upgrade", "proxy-authorization", "proxy-authenticate", "proxy-connection",
	}
)

// validateCredentials checks every grant and returns inject route names
// mapped to the credential that owns them.
func (s *Spec) validateCredentials(errs *fieldErrors) map[string]string {
	seen := map[string]bool{}
	routes := map[string]string{}
	for i := range s.Credentials {
		c := &s.Credentials[i]
		path := fmt.Sprintf("spec.credentials[%d]", i)
		validateName(errs, path+".name", c.Name, maxNameLength)
		if isValidName(c.Name) && seen[c.Name] {
			errs.add(path+".name", "duplicates another credential name")
		}
		seen[c.Name] = true
		switch c.Mode {
		case ModeMint:
			validateMint(errs, path, c)
		case ModeInject:
			s.validateInject(errs, path, c, routes)
		default:
			errs.add(path+".mode", "must be %q or %q", ModeMint, ModeInject)
		}
	}
	return routes
}

func validateMint(errs *fieldErrors, path string, c *Credential) {
	if app := c.GitHubApp; app == nil {
		errs.add(path+".githubApp", "is required")
	} else {
		validateGitHubApp(errs, path+".githubApp", app)
	}
	validateDelivery(errs, path, c)
	if c.Secret != "" || c.Route != nil {
		errs.add(path, "secret and route are only valid for inject credentials")
	}
}

func validateGitHubApp(errs *fieldErrors, path string, app *GitHubApp) {
	if !digitsPattern.MatchString(app.AppID) || !digitsPattern.MatchString(app.InstallationID) {
		errs.add(path, "appId and installationId must be numeric")
	}
	validateSecretRef(errs, path+".privateKey", app.PrivateKey)
	validateScope(errs, path+".scope", app.Scope)
}

func validateDelivery(errs *fieldErrors, path string, c *Credential) {
	if c.TTL.Duration <= 0 || c.TTL.Duration > maxMintTTL {
		errs.add(path+".ttl", "must be positive and at most %s", maxMintTTL)
	}
	for i, d := range c.Deliver {
		if d != DeliverGitHelper && d != DeliverGHEnv {
			errs.add(fmt.Sprintf("%s.deliver[%d]", path, i), "unknown target; use %q or %q",
				DeliverGitHelper, DeliverGHEnv)
		}
	}
	addDuplicates(errs, plainPath(path+".deliver"), duplicates(c.Deliver, identity), "target")
}

func validateScope(errs *fieldErrors, path string, scope *MintScope) {
	if scope == nil {
		errs.add(path, "is required")
		return
	}
	if len(scope.Repositories) == 0 {
		errs.add(path+".repositories", "must list at least one owner/repo")
	} else if len(scope.Repositories) > maxMintRepositories {
		errs.add(path+".repositories", "must list at most %d repositories", maxMintRepositories)
	}
	validateRepositories(errs, path+".repositories", scope.Repositories)
	validatePermissions(errs, path+".permissions", scope.Permissions)
}

// One installation token covers a single owner.
func validateRepositories(errs *fieldErrors, path string, repos []string) {
	owner := ""
	for i, repo := range repos {
		o, name, _ := strings.Cut(repo, "/")
		if !repositoryPattern.MatchString(repo) || isDotName(o) || isDotName(name) {
			errs.add(fmt.Sprintf("%s[%d]", path, i), "must be owner/repo")
			continue
		}
		if owner == "" {
			owner = o
		} else if !strings.EqualFold(o, owner) {
			errs.add(path, "all repositories must have the same owner")
		}
	}
	// GitHub repository names are case-insensitive.
	addDuplicates(errs, plainPath(path), duplicates(repos, strings.ToLower), "repository")
}

func isDotName(s string) bool {
	return s == "." || s == ".."
}

func validatePermissions(errs *fieldErrors, path string, permissions map[string]string) {
	if len(permissions) == 0 {
		errs.add(path, "must list at least one permission")
	}
	// Names are never shown: a key may be a pasted token, even one shaped
	// like a permission name. The line still points at the entry.
	for _, name := range sortedKeys(permissions) {
		level := permissions[name]
		at := fieldPath{key: path, shown: path}.entry(name, hiddenKey)
		highest, ok := permissionLevels[name]
		switch {
		case !ok:
			errs.addAt(at, "names must be one of %s", strings.Join(allowedPermissions, ", "))
		case level != levelRead && level != levelWrite:
			errs.addAt(at, "levels must be read or write")
		case level == levelWrite && highest == levelRead:
			errs.addAt(at, "actions, attestations, environments and metadata allow read only")
		}
	}
}

func isValidName(name string) bool {
	return namePattern.MatchString(name) && len(name) <= maxNameLength
}

func (s *Spec) validateInject(
	errs *fieldErrors,
	path string,
	c *Credential,
	routes map[string]string,
) {
	validateSecretRef(errs, path+".secret", c.Secret)
	if hasMintFields(c) {
		errs.add(
			path,
			"githubApp, ttl and deliver are only valid for mint credentials",
		)
	}
	if c.Route == nil {
		errs.add(path+".route", "is required")
		return
	}
	r := c.Route
	validateName(errs, path+".route.name", r.Name, maxNameLength)
	// Only valid names are registered, so env errors never name an invalid one.
	if isValidName(r.Name) {
		if _, dup := routes[r.Name]; dup {
			errs.add(path+".route.name", "duplicates another route name")
		} else {
			routes[r.Name] = c.Name
		}
	}
	validateHeader(errs, path+".route", r)
	s.validateUpstream(errs, path+".route.upstream", r.Upstream)
}

func hasMintFields(c *Credential) bool {
	return c.GitHubApp != nil || len(c.Deliver) > 0 || c.TTL.Duration != 0
}

func validateHeader(errs *fieldErrors, path string, r *Route) {
	if !httpguts.ValidHeaderFieldName(r.Header) ||
		slices.Contains(reservedHeaders, strings.ToLower(r.Header)) {
		errs.add(
			path+".header",
			"must be a header name other than %s",
			strings.Join(reservedHeaders, ", "),
		)
	}
	if !prefixPattern.MatchString(r.Prefix) {
		errs.add(path+".prefix", "must be at most 64 printable ASCII characters")
	}
}

func (s *Spec) validateUpstream(errs *fieldErrors, path, upstream string) {
	host, msg := checkUpstreamURL(upstream)
	if msg != "" {
		errs.add(path, "%s", msg)
		return
	}
	// A direct allow rule would let the agent reach the upstream with a key
	// of its own, bypassing the injected credential.
	if s.hostAllowed(host) {
		errs.add(path, "host must not also be matched by spec.egress.allow")
	}
}

func checkUpstreamURL(upstream string) (string, string) {
	u, err := url.Parse(upstream)
	if err != nil || !plainHTTPSOrigin(u, upstream) {
		return "", "must be https://host[:port] with no credentials, path, query or fragment"
	}
	if msg := checkURLPort(u.Port()); msg != "" {
		return "", msg
	}
	if msg := checkHostName(u.Hostname()); msg != "" {
		return "", "host " + msg
	}
	return u.Hostname(), ""
}

func plainHTTPSOrigin(u *url.URL, raw string) bool {
	return u.Scheme == "https" && u.User == nil && u.Path == "" &&
		!strings.ContainsAny(raw, "?#") && (!strings.HasSuffix(u.Host, ":") || u.Port() != "")
}

func checkURLPort(port string) string {
	if port == "" {
		return ""
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > maxPort {
		return fmt.Sprintf("port is outside 1 to %d", maxPort)
	}
	return ""
}

// validateSecretRef requires a qualified reference with a known type, since
// a pasted raw key often parses as a bare name, and never echoes the value,
// which would otherwise land in terminal and CI logs.
func validateSecretRef(errs *fieldErrors, path, ref string) {
	parsed, err := secrets.ParseRef(ref)
	if err != nil || !strings.Contains(ref, ":") || ref != strings.TrimSpace(ref) ||
		!slices.Contains(secretRefTypes, parsed.Type) ||
		(parsed.Type == secrets.LocalSourceName && parsed.Source != secrets.LocalSourceName) {
		errs.add(path, "must be a TYPE:SOURCE/NAME reference with type %s, such as %s",
			strings.Join(secretRefTypes, " or "), "sops:pool/API_KEY")
	}
}

func defaultCredential(c *Credential) {
	if c.Mode != ModeMint {
		return
	}
	defaultDuration(&c.TTL, defaultMintTTL)
	if len(c.Deliver) == 0 {
		c.Deliver = []string{DeliverGitHelper}
	}
}

// hostAllowed checks host rules only. Whether a cidr rule covers an upstream
// depends on DNS, so the sidecar refuses that overlap at policy load and
// again on every upstream lookup, failing closed.
func (s *Spec) hostAllowed(host string) bool {
	for _, rule := range s.Egress.Allow {
		if rule.Host != "" && HostMatches(rule.Host, host) {
			return true
		}
	}
	return false
}

// validateAgentGrants checks that agents name declared credentials and that
// {{route.<name>}} placeholders in their env use a route they were granted.
func (s *Spec) validateAgentGrants(errs *fieldErrors, routes map[string]string) {
	declared := make(map[string]bool, len(s.Credentials))
	for _, c := range s.Credentials {
		declared[c.Name] = true
	}
	for _, name := range sortedKeys(s.Agents) {
		agent := s.Agents[name]
		path := agentPath(name)
		for i, cred := range agent.Credentials {
			if !declared[cred] {
				errs.addAt(path.field("credentials").index(i),
					"must reference a declared credential")
			}
		}
		addDuplicates(errs, path.field("credentials"),
			duplicates(agent.Credentials, identity), "credential")
		for _, key := range sortedKeys(agent.Env) {
			if msg := checkEnvValue(agent.Env[key], agent, routes); msg != "" {
				errs.addAt(envPath(path, key), "%s", msg)
			}
		}
	}
}

// checkEnvValue never echoes the value or expression, since env values may
// hold a pasted key. Only declared route names appear in messages.
func checkEnvValue(value string, agent Agent, routes map[string]string) string {
	left, msg := stripPlaceholders(value, agent, routes)
	if msg != "" {
		return msg
	}
	// A lone "}}" is allowed so nested JSON values pass.
	if strings.Contains(left, "{{") || strayRouteRegex.MatchString(left) {
		return malformedPlaceholder
	}
	return ""
}

// stripPlaceholders checks each {{route.<name>}} and returns the text around them.
func stripPlaceholders(value string, agent Agent, routes map[string]string) (string, string) {
	var rest strings.Builder
	last := 0
	for _, m := range placeholderRegex.FindAllStringSubmatchIndex(value, -1) {
		if (m[0] > 0 && value[m[0]-1] == '{') || (m[1] < len(value) && value[m[1]] == '}') {
			return "", malformedPlaceholder
		}
		if msg := checkPlaceholder(value[m[2]:m[3]], agent, routes); msg != "" {
			return "", msg
		}
		rest.WriteString(value[last:m[0]])
		last = m[1]
	}
	rest.WriteString(value[last:])
	return rest.String(), ""
}

func checkPlaceholder(expr string, agent Agent, routes map[string]string) string {
	routeName, ok := strings.CutPrefix(expr, "route.")
	if !ok {
		return "unknown placeholder; only {{route.<name>}} is supported"
	}
	owner, found := routes[routeName]
	if !found {
		return "names undeclared route"
	}
	if !slices.Contains(agent.Credentials, owner) {
		return "uses a route whose credential the agent does not list"
	}
	return ""
}
