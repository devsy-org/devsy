package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// syncTestServer stands up an httptest automation API that records PATCHes so a test
// can assert what syncWith would push to the real cloud.
type syncTestServer struct {
	t          *testing.T
	srv        *httptest.Server
	promptByID map[string]string
}

func newSyncTestServer(t *testing.T, automations []cloudAutomation) *syncTestServer {
	t.Helper()
	ts := &syncTestServer{t: t, promptByID: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/automation/v1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewEncoder(w).Encode(listAutomationsResponse{
			Automations: automations,
			Total:       len(automations),
		})
	})
	mux.HandleFunc("/api/automation/v1/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/automation/v1/")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var patch map[string]string
		if err := json.Unmarshal(body, &patch); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ts.promptByID[id] = patch["prompt"]
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
	})
	ts.srv = httptest.NewServer(mux)
	t.Cleanup(ts.srv.Close)
	return ts
}

// tempAgentsDir creates an isolated .agents/agents root under t.TempDir so test
// fixtures never touch the committed .agents/ tree (which TestAgentsStructure in the
// root package scans in parallel).
func tempAgentsDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeAgentFile creates a minimal <agentsDir>/<id>/agent.md so syncWith can read it
// without depending on the generator having run first.
func writeAgentFile(t *testing.T, agentsDir, id, content string) {
	t.Helper()
	dir := filepath.Join(agentsDir, id)
	p := filepath.Join(dir, "agent.md")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSyncWithPatchesDriftedPrompt(t *testing.T) {
	agentsDir := tempAgentsDir(t)
	const id = "sync-demo"
	const name = "Daily Sync Demo"
	newPrompt := "---\nid: " + id + "\n---\nnew prompt body"
	writeAgentFile(t, agentsDir, id, newPrompt)

	stale := cloudAutomation{ID: "abc123", Name: name, Prompt: "old prompt body", Enabled: true}
	ts := newSyncTestServer(t, []cloudAutomation{stale})

	cfg := config{Agents: []agent{{ID: id, AutomationName: name}}}
	client := newSyncClientWith(ts.srv.URL, "test-token")
	if err := syncWith(cfg, client, agentsDir, false); err != nil {
		t.Fatalf("syncWith: %v", err)
	}
	if got := ts.promptByID["abc123"]; got != newPrompt {
		t.Errorf("patched prompt = %q, want %q", got, newPrompt)
	}
	if syncDriftExit {
		t.Errorf("apply run should not signal drift exit")
	}
}

func TestSyncDryRunReportsDriftWithoutPatching(t *testing.T) {
	agentsDir := tempAgentsDir(t)
	const id = "sync-dry"
	const name = "Daily Sync Dry"
	writeAgentFile(t, agentsDir, id, "local-only prompt")

	stale := cloudAutomation{ID: "xyz", Name: name, Prompt: "cloud prompt", Enabled: true}
	ts := newSyncTestServer(t, []cloudAutomation{stale})

	cfg := config{Agents: []agent{{ID: id, AutomationName: name}}}
	client := newSyncClientWith(ts.srv.URL, "test-token")
	syncDriftExit = false
	t.Cleanup(func() { syncDriftExit = false })
	if err := syncWith(cfg, client, agentsDir, true); err != nil {
		t.Fatalf("syncWith dry-run: %v", err)
	}
	if !syncDriftExit {
		t.Errorf("dry run with drift should signal non-zero exit")
	}
	if len(ts.promptByID) != 0 {
		t.Errorf("dry run must not patch, got %d patches", len(ts.promptByID))
	}
}

func TestSyncUpToDateIsNoop(t *testing.T) {
	agentsDir := tempAgentsDir(t)
	const id = "sync-ok"
	const name = "Daily Sync OK"
	body := "identical prompt"
	writeAgentFile(t, agentsDir, id, body)

	ts := newSyncTestServer(t, []cloudAutomation{
		{ID: "ok1", Name: name, Prompt: body, Enabled: true},
	})
	cfg := config{Agents: []agent{{ID: id, AutomationName: name}}}
	client := newSyncClientWith(ts.srv.URL, "test-token")
	if err := syncWith(cfg, client, agentsDir, false); err != nil {
		t.Fatalf("syncWith: %v", err)
	}
	if len(ts.promptByID) != 0 {
		t.Errorf("up-to-date automation must not be patched")
	}
}

func TestSyncMissingAndDisabledReported(t *testing.T) {
	agentsDir := tempAgentsDir(t)
	const missingID = "sync-missing"
	const disabledID = "sync-disabled"
	writeAgentFile(t, agentsDir, missingID, "p")
	writeAgentFile(t, agentsDir, disabledID, "p")

	ts := newSyncTestServer(t, []cloudAutomation{
		{ID: "dis1", Name: "Daily Sync Disabled", Prompt: "p", Enabled: false},
	})
	cfg := config{Agents: []agent{
		{ID: missingID, AutomationName: "Daily Sync Missing"},
		{ID: disabledID, AutomationName: "Daily Sync Disabled"},
	}}
	client := newSyncClientWith(ts.srv.URL, "test-token")
	if err := syncWith(cfg, client, agentsDir, false); err != nil {
		t.Fatalf("syncWith: %v", err)
	}
	if len(ts.promptByID) != 0 {
		t.Errorf("missing/disabled automations must not be patched")
	}
}

func TestAutomationTokenRequired(t *testing.T) {
	t.Setenv("OPENHANDS_API_KEY", "")
	t.Setenv("OPENHANDS_CLOUD_API_KEY", "")
	if _, err := newSyncClient(); err == nil {
		t.Errorf("newSyncClient without a token should error")
	}
}

func TestSyncAuthHeaderSent(t *testing.T) {
	agentsDir := tempAgentsDir(t)
	const id = "sync-auth"
	const name = "Daily Sync Auth"
	writeAgentFile(t, agentsDir, id, "p")

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(listAutomationsResponse{
			Automations: []cloudAutomation{{ID: "a1", Name: name, Prompt: "p", Enabled: true}},
			Total:       1,
		})
	}))
	t.Cleanup(srv.Close)

	cfg := config{Agents: []agent{{ID: id, AutomationName: name}}}
	client := newSyncClientWith(srv.URL, "secret-token")
	if err := syncWith(cfg, client, agentsDir, false); err != nil {
		t.Fatalf("syncWith: %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization header = %q, want Bearer secret-token", gotAuth)
	}
}
