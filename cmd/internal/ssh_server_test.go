package cmdinternal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/token"
	"github.com/devsy-org/ssh"
)

func encodeTestToken(t *testing.T, tok token.Token) string {
	t.Helper()
	raw, err := json.Marshal(&tok)
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

const testEd25519PubKey = "ssh-ed25519 " +
	"AAAAC3NzaC1lZDI1NTE5AAAAIDuxlhheJj+ON3HxiToVhg+Tj1+/cqLgkBQ8KkKr2T87 " +
	"test@example"

func TestParseSSHTokenEmpty(t *testing.T) {
	keys, hostKey, err := parseSSHToken("")
	if err != nil || keys != nil || hostKey != nil {
		t.Fatalf("expected zeroes, got keys=%v hostKey=%v err=%v", keys, hostKey, err)
	}
}

func TestParseSSHTokenInvalid(t *testing.T) {
	_, _, err := parseSSHToken("not-a-valid-token-blob")
	if err == nil || !strings.Contains(err.Error(), "parse token") {
		t.Fatalf("want parse error, got %v", err)
	}
}

func TestParseSSHTokenWithKeys(t *testing.T) {
	encoded := encodeTestToken(t, token.Token{
		AuthorizedKeys: base64.StdEncoding.EncodeToString([]byte(testEd25519PubKey + "\n")),
		HostKey:        base64.StdEncoding.EncodeToString([]byte("hostkeybytes")),
	})
	keys, hostKey, err := parseSSHToken(encoded)
	if err != nil {
		t.Fatalf("parseSSHToken: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("want 1 key, got %d", len(keys))
	}
	if string(hostKey) != "hostkeybytes" {
		t.Errorf("hostKey = %q", hostKey)
	}
}

func TestDecodeAuthorizedKeysMultiple(t *testing.T) {
	blob := testEd25519PubKey + "\n" + testEd25519PubKey + "\n"
	keys, err := decodeAuthorizedKeys(base64.StdEncoding.EncodeToString([]byte(blob)))
	if err != nil {
		t.Fatalf("decodeAuthorizedKeys: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("want 2 keys, got %d", len(keys))
	}
}

func TestDecodeAuthorizedKeysBadBase64(t *testing.T) {
	_, err := decodeAuthorizedKeys("!!!not-base64!!!")
	if err == nil || !strings.Contains(err.Error(), "decode authorized keys") {
		t.Fatalf("want wrapped decode error, got %v", err)
	}
}

func TestDecodeBase64BytesEmpty(t *testing.T) {
	out, err := decodeBase64Bytes("", "host key")
	if err != nil || out != nil {
		t.Fatalf("want (nil,nil), got (%v,%v)", out, err)
	}
}

func TestDecodeBase64BytesError(t *testing.T) {
	_, err := decodeBase64Bytes("!!!", "host key")
	if err == nil || !strings.Contains(err.Error(), "decode host key") {
		t.Fatalf("want wrapped error with label, got %v", err)
	}
}

func TestEnsureActivityFileCreatesAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity")
	if err := ensureActivityFile(path); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}
	// idempotent
	if err := ensureActivityFile(path); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

func TestEnsureActivityFileSurfacesNonExistErrors(t *testing.T) {
	// Path through a non-directory is ENOTDIR, which is neither nil nor ErrNotExist —
	// confirm we surface it instead of trying to create.
	tmp := t.TempDir()
	regular := filepath.Join(tmp, "regular")
	if err := os.WriteFile(regular, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(regular, "child")
	err := ensureActivityFile(bad)
	if err == nil {
		t.Fatal("expected error for path under a regular file")
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Errorf("want non-ErrNotExist error surfaced, got %v", err)
	}
}

type fakeServer struct {
	shutdownCalls int
	shutdownErr   error
}

func (*fakeServer) Serve(net.Listener) error { return nil }
func (*fakeServer) ListenAndServe() error    { return nil }
func (f *fakeServer) Shutdown(context.Context) error {
	f.shutdownCalls++
	return f.shutdownErr
}

func TestShutdownOnCancelInvokesServer(t *testing.T) {
	f := &fakeServer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		shutdownOnCancel(ctx, f)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdownOnCancel did not return after ctx cancel")
	}
	if f.shutdownCalls != 1 {
		t.Errorf("Shutdown calls = %d, want 1", f.shutdownCalls)
	}
}

func TestIgnoreServerClosed(t *testing.T) {
	if err := ignoreServerClosed(nil); err != nil {
		t.Errorf("nil should map to nil, got %v", err)
	}
	if err := ignoreServerClosed(ssh.ErrServerClosed); err != nil {
		t.Errorf("ErrServerClosed should map to nil, got %v", err)
	}
	other := errors.New("boom")
	if err := ignoreServerClosed(other); err != other {
		t.Errorf("unrelated error should pass through, got %v", err)
	}
}

func TestRunActivityHeartbeatExitsOnContextCancel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runActivityHeartbeat(ctx, path)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat did not exit within 2s of context cancel")
	}
}
