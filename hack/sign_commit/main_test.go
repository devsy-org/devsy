package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return key
}

const (
	testSubject = "subject"
	testSummary = "summary"
)

func TestSplitMessage(t *testing.T) {
	cases := []struct {
		name, message, body, wantHead, wantBody string
	}{
		{"body overrides", testSubject, "the body", testSubject, "the body"},
		{"single line", testSubject, "", testSubject, ""},
		{"multiline", testSubject + "\nline one\nline two", "", testSubject, "line one\nline two"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			head, body := splitMessage(c.message, c.body)
			if head != c.wantHead || body != c.wantBody {
				t.Errorf("got (%q, %q), want (%q, %q)", head, body, c.wantHead, c.wantBody)
			}
		})
	}
}

func TestStripCoAuthored(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{"no trailer", "plain body\ntext here", "plain body\ntext here"},
		{
			"trailing trailer",
			"summary line\n\nCo-authored-by: openhands <openhands@all-hands.dev>",
			"summary line",
		},
		{
			"mid-body trailer",
			"line one\nCo-authored-by: someone <x@y.com>\nline two",
			"line one\nline two",
		},
		{
			"case-insensitive",
			"summary\nco-authored-by: bot <bot@x.com>",
			testSummary,
		},
		{
			"leading whitespace",
			"summary\n  Co-authored-by: bot <bot@x.com>",
			testSummary,
		},
		{
			"multiple trailers",
			"summary\nCo-authored-by: a <a@x.com>\nCo-authored-by: b <b@x.com>",
			testSummary,
		},
		{"empty after strip", "Co-authored-by: bot <bot@x.com>", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stripCoAuthored(c.input)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestSanitizeSecrets(t *testing.T) {
	ghsToken := "ghs_1234567890abcdefghijklmnopqrstuvwxyz1234"
	ghoToken := "gho_1234567890abcdefghijklmnopqrstuvwxyz1234"
	ghpToken := "ghp_1234567890abcdefghijklmnopqrstuvwxyz1234"
	jwtToken := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9" +
		".eyJzdWIiOiIxMjM0NTY3ODkwIn0" +
		".SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	shortToken := "ghs_abc123XYZabcdefghijklmnopqrstuvwxyz1234"

	cases := []struct {
		name, input, want string
	}{
		{"no secrets", "plain commit body", "plain commit body"},
		{"ghs token", "token is " + ghsToken + " here", "token is [REDACTED] here"},
		{"gho token", "auth: " + ghoToken, "auth: [REDACTED]"},
		{"ghp token", ghpToken, "[REDACTED]"},
		{"jwt token", "jwt: " + jwtToken, "jwt: [REDACTED]"},
		{"multiple", ghsToken + " and " + jwtToken, "[REDACTED] and [REDACTED]"},
		{"embedded in sentence", "via " + shortToken + ", the tool", "via [REDACTED], the tool"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sanitizeSecrets(c.input)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestSanitizeSecretsInSplitMessage(t *testing.T) {
	ghsToken := "ghs_1234567890abcdefghijklmnopqrstuvwxyz1234"
	msg := "subject\nbody with " + ghsToken + " token"
	head, body := splitMessage(msg, "")
	if head != "subject" {
		t.Errorf("headline: got %q", head)
	}
	if strings.Contains(body, ghsToken) {
		t.Errorf("body still contains ghs token: %q", body)
	}
	if !strings.Contains(body, "[REDACTED]") {
		t.Errorf("body should contain [REDACTED]: %q", body)
	}
}

func TestSplitMessageStripsCoAuthored(t *testing.T) {
	msg := "subject\nbody text\n\nCo-authored-by: openhands <openhands@all-hands.dev>"
	head, body := splitMessage(msg, "")
	if head != "subject" {
		t.Errorf("headline: got %q, want %q", head, "subject")
	}
	if strings.Contains(body, "Co-authored-by") {
		t.Errorf("body still contains Co-authored-by: %q", body)
	}
	if body != "body text" {
		t.Errorf("body: got %q, want %q", body, "body text")
	}
}

func TestSplitMessageStripsCoAuthoredFromBody(t *testing.T) {
	head, body := splitMessage(
		"subject",
		"body text\n\nCo-authored-by: openhands <openhands@all-hands.dev>",
	)
	if strings.Contains(head, "Co-authored-by") {
		t.Errorf("headline contains Co-authored-by: %q", head)
	}
	if strings.Contains(body, "Co-authored-by") {
		t.Errorf("body still contains Co-authored-by: %q", body)
	}
}

func TestFileChanges(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(a, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("world!"), 0o600); err != nil {
		t.Fatal(err)
	}

	adds, dels, err := fileChanges([]string{a, b}, os.ReadFile)
	if err != nil {
		t.Fatalf("fileChanges: %v", err)
	}
	if len(dels) != 0 {
		t.Fatalf("got %d deletions, want 0", len(dels))
	}
	if len(adds) != 2 {
		t.Fatalf("got %d additions, want 2", len(adds))
	}
	want := []addition{
		{Path: a, Contents: "aGVsbG8="},
		{Path: b, Contents: "d29ybGQh"},
	}
	if !reflect.DeepEqual(adds, want) {
		t.Errorf("got %+v, want %+v", adds, want)
	}
}

func TestFileChangesMissingFileIsDeletion(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	adds, dels, err := fileChanges([]string{missing}, os.ReadFile)
	if err != nil {
		t.Fatalf("fileChanges: %v", err)
	}
	if len(adds) != 0 {
		t.Fatalf("got %d additions, want 0", len(adds))
	}
	if len(dels) != 1 || dels[0].Path != missing {
		t.Fatalf("got deletions %+v, want [{%s}]", dels, missing)
	}
}

func TestFileChangesBase64(t *testing.T) {
	reader := func(string) ([]byte, error) { return []byte{0x00, 0xff}, nil }
	adds, _, err := fileChanges([]string{"x"}, reader)
	if err != nil {
		t.Fatalf("fileChanges: %v", err)
	}
	if adds[0].Contents != "AP8=" {
		t.Errorf("got %q, want AP8=", adds[0].Contents)
	}
}

func TestResolvePathsExplicit(t *testing.T) {
	got, err := resolvePaths(false, []string{"a", "b"})
	if err != nil {
		t.Fatalf("resolvePaths: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("got %v, want [a b]", got)
	}
}

func TestResolvePathsNoAll(t *testing.T) {
	if _, err := resolvePaths(false, nil); err == nil {
		t.Fatal("expected error when no paths and all=false")
	}
}

func TestCommitVars(t *testing.T) {
	o := options{message: testSubject, body: "body text", repo: "devsy-org/devsy"}
	changes := fileChange{
		Additions: []addition{{Path: "f", Contents: "AA=="}},
		Deletions: []deletion{{Path: "g"}},
	}
	vars := commitVars(o, "mybranch", "deadbeef", changes)
	in := vars.Input
	checkBranch := func(wantRepo, wantName string) {
		t.Helper()
		if in.Branch.Repo != wantRepo {
			t.Errorf("repo: got %q", in.Branch.Repo)
		}
		if in.Branch.Name != wantName {
			t.Errorf("branch name: got %q", in.Branch.Name)
		}
	}
	checkField := func(label, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s: got %q, want %q", label, got, want)
		}
	}
	checkBranch("devsy-org/devsy", "refs/heads/mybranch")
	checkField("head", in.ExpectedHead, "deadbeef")
	checkField("headline", in.Message.Headline, "subject")
	checkField("body", in.Message.Body, "body text")
	if len(in.FileChanges.Additions) != 1 || in.FileChanges.Additions[0].Path != "f" {
		t.Errorf("additions: got %+v", in.FileChanges.Additions)
	}
	if len(in.FileChanges.Deletions) != 1 || in.FileChanges.Deletions[0].Path != "g" {
		t.Errorf("deletions: got %+v", in.FileChanges.Deletions)
	}
}

func TestGenerateJWT(t *testing.T) {
	key := testKey(t)
	now := time.Now()
	tokenStr, err := generateJWT("123456", key, now)
	if err != nil {
		t.Fatalf("generateJWT: %v", err)
	}
	claims := &jwt.RegisteredClaims{}
	parsed, err := jwt.ParseWithClaims(tokenStr, claims, func(*jwt.Token) (any, error) {
		return &key.PublicKey, nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("parse jwt: %v valid=%v", err, parsed.Valid)
	}
	if claims.Issuer != "123456" {
		t.Errorf("iss: got %q", claims.Issuer)
	}
}

func TestGenerateJWTMissingID(t *testing.T) {
	if _, err := generateJWT("", testKey(t), time.Now()); err == nil {
		t.Fatal("expected error for missing app id")
	}
}

func TestLoadPrivateKeyMissing(t *testing.T) {
	if _, err := loadPrivateKey("", ""); err == nil {
		t.Fatal("expected error when no key provided")
	}
}

func TestLoadPrivateKeyFromContents(t *testing.T) {
	key := testKey(t)
	loaded, err := loadPrivateKey("", string(rsaPEM(t, key)))
	if err != nil {
		t.Fatalf("loadPrivateKey: %v", err)
	}
	if loaded.N.Cmp(key.N) != 0 {
		t.Error("loaded key does not match")
	}
}

func TestInstallationURL(t *testing.T) {
	const want = "https://api.github.com/orgs/devsy-org/installation"
	if got := installationURL("devsy-org"); got != want {
		t.Errorf("got %q", got)
	}
}

func TestSplitLines(t *testing.T) {
	if got := splitLines(""); len(got) != 0 {
		t.Errorf("empty: got %v", got)
	}
	got := splitLines("a\nb\nc")
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("got %v", got)
	}
}

func TestStripDashDash(t *testing.T) {
	const tok = "-token"
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"no separator", []string{tok}, []string{tok}},
		{"leading separator", []string{"--", tok}, []string{tok}},
		{
			"separator with multiple flags",
			[]string{"--", "-m", "msg", "-b", "body"},
			[]string{"-m", "msg", "-b", "body"},
		},
		{"no args", nil, nil},
		{"separator only", []string{"--"}, []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stripDashDash(c.args)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestUrlOrErr(t *testing.T) {
	if got := urlOrErr(map[string]any{"message": "bad"}); got != "bad" {
		t.Errorf("got %q", got)
	}
	if got := urlOrErr(map[string]any{}); got != "" {
		t.Errorf("got %q", got)
	}
}

func rsaPEM(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	der := x509.MarshalPKCS1PrivateKey(key)
	enc := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
	if !strings.HasPrefix(string(enc), "-----BEGIN") {
		t.Fatalf("unexpected pem: %s", enc)
	}
	return enc
}
