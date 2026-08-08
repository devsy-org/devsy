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

const testSubject = "subject"

func TestSplitMessage(t *testing.T) {
	cases := []struct {
		name, message, body, wantHead, wantBody string
	}{
		{"body overrides", testSubject, "the body", testSubject, "the body"},
		{"single line", testSubject, "", testSubject, ""},
		{"multiline", "subject\nline one\nline two", "", "subject", "line one\nline two"},
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

func TestAdditions(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(a, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("world!"), 0o600); err != nil {
		t.Fatal(err)
	}

	adds, err := additions([]string{a, b}, os.ReadFile)
	if err != nil {
		t.Fatalf("additions: %v", err)
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

func TestAdditionsMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := additions([]string{missing}, os.ReadFile); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestAdditionsBase64(t *testing.T) {
	reader := func(string) ([]byte, error) { return []byte{0x00, 0xff}, nil }
	adds, err := additions([]string{"x"}, reader)
	if err != nil {
		t.Fatalf("additions: %v", err)
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
	adds := []addition{{Path: "f", Contents: "AA=="}}
	vars := commitVars(o, "mybranch", "deadbeef", adds)
	in := vars.Input
	if in.Branch.Repo != "devsy-org/devsy" {
		t.Errorf("repo: got %q", in.Branch.Repo)
	}
	if in.Branch.Name != "refs/heads/mybranch" {
		t.Errorf("branch name: got %q", in.Branch.Name)
	}
	if in.ExpectedHead != "deadbeef" {
		t.Errorf("head: got %q", in.ExpectedHead)
	}
	if in.Message.Headline != "subject" || in.Message.Body != "body text" {
		t.Errorf("message: got %+v", in.Message)
	}
	if len(in.FileChanges.Additions) != 1 || in.FileChanges.Additions[0].Path != "f" {
		t.Errorf("additions: got %+v", in.FileChanges.Additions)
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
