package dockerwsl

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf16"
)

const (
	hostKeyType = "ssh-ed25519"

	sshModeAlias  = "alias"
	sshModeDirect = "direct"
)

var runIDUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// sshHostSpec describes the Host block the Docker CLI's ssh.exe will match.
type sshHostSpec struct {
	RunID        string
	Alias        string
	HostName     string
	Port         string
	IdentityFile string
	KnownHosts   string
	HostKeyAlias bool
}

func sshBlockBegin(runID string) string { return "# BEGIN devsy-ci " + runID + "\n" }

func sshBlockEnd(runID string) string { return "# END devsy-ci " + runID + "\n" }

// sshConfigBlock renders a tightly scoped block: key-only, strict host-key
// checking against an isolated known_hosts file, no agent or password use.
func sshConfigBlock(spec sshHostSpec) string {
	lines := []string{
		"Host " + spec.Alias,
		"  HostName " + spec.HostName,
		"  User " + ciUser,
		"  Port " + spec.Port,
		"  IdentityFile " + sshConfigPath(spec.IdentityFile),
		"  IdentitiesOnly yes",
	}
	if spec.HostKeyAlias {
		lines = append(lines, "  HostKeyAlias "+spec.Alias)
	}
	lines = append(lines,
		"  UserKnownHostsFile "+sshConfigPath(spec.KnownHosts),
		"  StrictHostKeyChecking yes",
		"  CheckHostIP no",
		"  PasswordAuthentication no",
		"  BatchMode yes",
	)
	return sshBlockBegin(spec.RunID) + strings.Join(lines, "\n") + "\n" + sshBlockEnd(spec.RunID)
}

// sshConfigPath renders a path for ssh_config: forward slashes, quoted if it
// contains whitespace.
func sshConfigPath(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if strings.ContainsAny(p, " \t") {
		return `"` + p + `"`
	}
	return p
}

// prependSSHBlock puts the block (plus one separating blank line) before the
// user's original configuration, so first-match-wins semantics apply to it.
func prependSSHBlock(original []byte, block string) []byte {
	out := make([]byte, 0, len(block)+1+len(original))
	out = append(out, block...)
	out = append(out, '\n')
	return append(out, original...)
}

// stripSSHBlock removes the block this run prepended. It reports false when the
// config does not start with that run's block.
func stripSSHBlock(current []byte, runID string) ([]byte, bool) {
	begin, end := sshBlockBegin(runID), sshBlockEnd(runID)
	if !bytes.HasPrefix(current, []byte(begin)) {
		return current, false
	}
	idx := bytes.Index(current, []byte(end))
	if idx < 0 {
		return current, false
	}
	rest := current[idx+len(end):]
	if !bytes.HasPrefix(rest, []byte("\n")) {
		return current, false
	}
	return rest[1:], true
}

// knownHostsContent pins the server key to the names ssh.exe may look up.
func knownHostsContent(spec sshHostSpec, hostKey string) string {
	var b strings.Builder
	if spec.HostKeyAlias {
		fmt.Fprintf(&b, "%s %s\n", spec.Alias, hostKey)
		fmt.Fprintf(&b, "[%s]:%s %s\n", spec.Alias, spec.Port, hostKey)
		return b.String()
	}
	fmt.Fprintf(&b, "[%s]:%s %s\n", spec.HostName, spec.Port, hostKey)
	return b.String()
}

// parseHostKey extracts "ssh-ed25519 <base64>" from a .pub file, dropping the
// comment, and validates the key blob.
func parseHostKey(pub string) (string, error) {
	fields := strings.Fields(pub)
	if len(fields) < 2 || fields[0] != hostKeyType {
		return "", fmt.Errorf("host public key is not %s", hostKeyType)
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil || !bytes.Contains(blob, []byte(hostKeyType)) {
		return "", fmt.Errorf("host public key blob is not a valid %s key", hostKeyType)
	}
	return fields[0] + " " + fields[1], nil
}

// parseIdentityPublicKey validates the Windows-generated public key before it
// is handed to WSL.
func parseIdentityPublicKey(pub []byte) (string, error) {
	key, err := parseHostKey(string(pub))
	if err != nil {
		return "", fmt.Errorf("generated identity: %w", err)
	}
	return key, nil
}

// icaclsRestrictArgs drops the inherited entries and sets the user's own entry
// to read-only. Explicit entries of other principals survive it.
func icaclsRestrictArgs(key, user string) []string {
	return []string{key, "/inheritance:r", "/grant:r", user + ":(R)"}
}

// icaclsRemoveArgs deletes every entry of principal from the key's ACL.
func icaclsRemoveArgs(key, principal string) []string {
	return []string{key, "/remove", principal}
}

// aclPrincipals lists, in order, the principal of each entry in `icacls <key>`
// output.
func aclPrincipals(output, key string) []string {
	var principals []string
	for line := range strings.Lines(output) {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), key))
		if line == "" || strings.HasPrefix(line, "Successfully") {
			continue
		}
		if principal, _, ok := strings.Cut(line, ":("); ok {
			principals = append(principals, principal)
		}
	}
	return principals
}

// foreignPrincipals lists the principals in the ACL other than user, once each.
func foreignPrincipals(output, key, user string) []string {
	var foreign []string
	for _, principal := range aclPrincipals(output, key) {
		name := principal[strings.LastIndex(principal, `\`)+1:]
		if !strings.EqualFold(name, user) && !slices.Contains(foreign, principal) {
			foreign = append(foreign, principal)
		}
	}
	return foreign
}

// verifyKeyACL checks `icacls <key>` output: every entry must belong to user.
func verifyKeyACL(output, key, user string) error {
	if len(aclPrincipals(output, key)) == 0 {
		return fmt.Errorf("identity file ACL lists no entry for %s", user)
	}
	if foreign := foreignPrincipals(output, key, user); len(foreign) > 0 {
		return fmt.Errorf("identity file ACL grants access to %q", foreign[0])
	}
	return nil
}

// normalizeWSLOutput converts wsl.exe output to UTF-8 text. wsl.exe writes its
// own messages (distribution lists, errors) as UTF-16LE unless WSL_UTF8 is set;
// output relayed from Linux commands is already UTF-8.
func normalizeWSLOutput(raw string) string {
	data := []byte(raw)
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if bytes.IndexByte(data, 0) >= 0 {
		data = bytes.TrimPrefix(data, []byte{0xff, 0xfe})
		if len(data)%2 != 0 {
			data = data[:len(data)-1]
		}
		units := make([]uint16, 0, len(data)/2)
		for i := 0; i+1 < len(data); i += 2 {
			units = append(units, binary.LittleEndian.Uint16(data[i:]))
		}
		data = []byte(string(utf16.Decode(units)))
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

// parseWSLDistributions splits `wsl.exe -l -q` output into names.
func parseWSLDistributions(raw string) []string {
	var names []string
	for line := range strings.Lines(normalizeWSLOutput(raw)) {
		if name := strings.TrimSpace(strings.TrimPrefix(line, "\ufeff")); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// sanitizeRunID maps a CI job marker to a safe, bounded identifier.
func sanitizeRunID(raw string) string {
	id := strings.Trim(runIDUnsafe.ReplaceAllString(raw, "-"), "-._")
	if len(id) > maxRunIDLen {
		id = id[:maxRunIDLen]
	}
	return id
}
