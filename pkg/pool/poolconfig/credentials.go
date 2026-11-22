package poolconfig

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/secrets"
)

const (
	maxNameLength = 63
	// GitHub installation tokens expire after one hour and cannot be extended.
	maxMintTTL = time.Hour
)

var (
	digitsPattern     = regexp.MustCompile(`^[0-9]+$`)
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	permissionPattern = regexp.MustCompile(`^[a-z_]+$`)
	headerPattern     = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	reservedHeaders   = []string{
		"host",
		"proxy-authorization",
		"content-length",
		"transfer-encoding",
	}
	proxyEnvNames = []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY"}
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
		if seen[c.Name] {
			errs.add(path+".name", "duplicate credential %q", c.Name)
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
	if c.Issuer != IssuerGitHubApp {
		errs.add(path+".issuer", "must be %q", IssuerGitHubApp)
	}
	if !digitsPattern.MatchString(c.AppID) || !digitsPattern.MatchString(c.InstallationID) {
		errs.add(path, "appId and installationId must be numeric")
	}
	validateSecretRef(errs, path+".privateKey", c.PrivateKey)
	validateScope(errs, path+".scope", c.Scope)
	validateDelivery(errs, path, c)
	if c.Secret != "" || c.Route != nil {
		errs.add(path, "secret and route are only valid for inject credentials")
	}
}

func validateDelivery(errs *fieldErrors, path string, c *Credential) {
	if c.TTL.Duration <= 0 || c.TTL.Duration > maxMintTTL {
		errs.add(path+".ttl", "must be positive and at most %s", maxMintTTL)
	}
	for _, d := range c.Deliver {
		if d != DeliverGitHelper && d != DeliverGHEnv {
			errs.add(path+".deliver", "unknown target %q", d)
		}
	}
}

func validateScope(errs *fieldErrors, path string, scope *MintScope) {
	if scope == nil || len(scope.Repositories) == 0 {
		errs.add(path+".repositories", "must list at least one owner/repo")
		return
	}
	for _, repo := range scope.Repositories {
		if !repositoryPattern.MatchString(repo) {
			errs.add(path+".repositories", "%q is not owner/repo", repo)
		}
	}
	validatePermissions(errs, path+".permissions", scope.Permissions)
}

func validatePermissions(errs *fieldErrors, path string, permissions map[string]string) {
	if len(permissions) == 0 {
		errs.add(path, "must list at least one permission")
	}
	for name, level := range permissions {
		if !permissionPattern.MatchString(name) || (level != "read" && level != "write") {
			errs.add(path, "%q must be read or write", name)
		}
	}
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
			"issuer, privateKey, scope, ttl and deliver are only valid for mint credentials",
		)
	}
	if c.Route == nil {
		errs.add(path+".route", "is required")
		return
	}
	r := c.Route
	validateName(errs, path+".route.name", r.Name, maxNameLength)
	if _, dup := routes[r.Name]; dup {
		errs.add(path+".route.name", "duplicate route %q", r.Name)
	}
	routes[r.Name] = c.Name
	validateHeader(errs, path+".route.header", r.Header)
	s.validateUpstream(errs, path+".route.upstream", r.Upstream)
}

func hasMintFields(c *Credential) bool {
	return c.Issuer != "" || c.AppID != "" || c.InstallationID != "" || c.PrivateKey != "" ||
		c.Scope != nil || len(c.Deliver) > 0 || c.TTL.Duration != 0
}

func validateHeader(errs *fieldErrors, path, header string) {
	if !headerPattern.MatchString(header) ||
		slices.Contains(reservedHeaders, strings.ToLower(header)) {
		errs.add(path, "must be a header name other than %s", strings.Join(reservedHeaders, ", "))
	}
}

func (s *Spec) validateUpstream(errs *fieldErrors, path, upstream string) {
	u, err := url.Parse(upstream)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		errs.add(path, "must be an https URL without credentials, query or fragment")
		return
	}
	if msg := checkHostName(u.Hostname()); msg != "" {
		errs.add(path, "host %s", msg)
		return
	}
	// A direct allow rule would let the agent reach the upstream with a key
	// of its own, bypassing the injected credential.
	if s.hostAllowed(u.Hostname()) {
		errs.add(path, "host %q must not also be in spec.egress.allow", u.Hostname())
	}
}

func validateSecretRef(errs *fieldErrors, path, ref string) {
	if _, err := secrets.ParseRef(ref); err != nil {
		errs.add(path, "%v", err)
	}
}

func validateEnvName(name string) error {
	if err := secrets.ValidateName(name); err != nil {
		return err
	}
	if slices.Contains(proxyEnvNames, strings.ToUpper(name)) {
		return fmt.Errorf("%s is set by the pool and cannot be overridden", name)
	}
	return nil
}
