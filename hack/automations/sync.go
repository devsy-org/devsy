package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultAutomationHost = "https://app.all-hands.dev"
	automationListPath    = "/api/automation/v1"
	automationItemPath    = "/api/automation/v1/%s"
)

// cloudAutomation is the subset of the OpenHands automation record that sync needs.
type cloudAutomation struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Prompt  string `json:"prompt"`
	Enabled bool   `json:"enabled"`
}

type listAutomationsResponse struct {
	Automations []cloudAutomation `json:"automations"`
	Total       int               `json:"total"`
}

// automationHost resolves the API base URL from OPENHANDS_HOST, falling back to the
// hosted default. Mirrors the convention used by hack/analytics/analyze_runs.py.
func automationHost() string {
	if h := os.Getenv("OPENHANDS_HOST"); h != "" {
		return h
	}
	return defaultAutomationHost
}

// automationToken resolves the bearer token used to authenticate to the automation API.
func automationToken() (string, error) {
	if t := os.Getenv("OPENHANDS_API_KEY"); t != "" {
		return t, nil
	}
	if t := os.Getenv("OPENHANDS_CLOUD_API_KEY"); t != "" {
		return t, nil
	}
	return "", fmt.Errorf("OPENHANDS_API_KEY is not set; export it to sync automations")
}

// defaultAgentsDir is the on-disk root of generated agent.md files, relative to the
// repo root the binary is invoked from. SYNC_AGENTS_DIR overrides it for testing.
func defaultAgentsDir() string {
	if d := os.Getenv("SYNC_AGENTS_DIR"); d != "" {
		return d
	}
	return filepath.Join(".agents", "agents")
}

type syncClient struct {
	host  string
	token string
	hc    *http.Client
}

func newSyncClient() (*syncClient, error) {
	token, err := automationToken()
	if err != nil {
		return nil, err
	}
	return &syncClient{
		host:  automationHost(),
		token: token,
		hc:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// newSyncClientWith is the testable constructor: it lets a test point at an httptest
// server and supply a token without touching process-wide env vars.
func newSyncClientWith(host, token string) *syncClient {
	return &syncClient{host: host, token: token, hc: &http.Client{Timeout: 30 * time.Second}}
}

func (c *syncClient) do(method, path string, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	host := strings.TrimSuffix(c.host, "/")
	req, err := http.NewRequest(method, host+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return data, fmt.Errorf(
			"%s %s: http %d: %s",
			method,
			path,
			resp.StatusCode,
			truncate(string(data), 500),
		)
	}
	return data, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func (c *syncClient) listAutomations() ([]cloudAutomation, error) {
	var all []cloudAutomation
	limit := 50
	offset := 0
	for {
		path := fmt.Sprintf("%s?limit=%d&offset=%d", automationListPath, limit, offset)
		data, err := c.do(http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var page listAutomationsResponse
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, fmt.Errorf("parse automation list: %w", err)
		}
		all = append(all, page.Automations...)
		if len(page.Automations) < limit || len(all) >= page.Total {
			break
		}
		offset += len(page.Automations)
	}
	return all, nil
}

func (c *syncClient) patchPrompt(id, prompt string) error {
	body, err := json.Marshal(map[string]string{"prompt": prompt})
	if err != nil {
		return err
	}
	_, err = c.do(http.MethodPatch, fmt.Sprintf(automationItemPath, id), body)
	return err
}

// runSync pushes each generated agent.md prompt to its matching OpenHands automation.
// In dry-run mode it only reports drift. Agents whose cloud automation is missing or
// disabled are reported but not modified.
func runSync(cfg config, dryRun bool) {
	client, err := newSyncClient()
	if err != nil {
		fail("sync: %v", err)
	}
	if err := syncWith(cfg, client, defaultAgentsDir(), dryRun); err != nil {
		fail("sync: %v", err)
	}
	if syncDriftExit {
		os.Exit(1)
	}
}

// syncWith is the testable core: it lists automations from client, matches each agent
// to its cloud automation by name, and patches the prompt when it has drifted. It
// returns an error only for transport/API failures; drift is a normal outcome reported
// via the returned counts and stdout.
func syncWith(cfg config, client *syncClient, agentsDir string, dryRun bool) error {
	cloud, err := client.listAutomations()
	if err != nil {
		return fmt.Errorf("list automations: %w", err)
	}
	byName := make(map[string]cloudAutomation, len(cloud))
	for _, a := range cloud {
		if _, exists := byName[a.Name]; exists {
			return fmt.Errorf("duplicate cloud automation name: %q", a.Name)
		}
		byName[a.Name] = a
	}

	agents := append([]agent(nil), cfg.Agents...)
	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })

	ctx := syncAgentContext{byName: byName, client: client, agentsDir: agentsDir, dryRun: dryRun}
	var counts [numStatus]int
	for _, a := range agents {
		status, err := syncAgent(a, ctx)
		if err != nil {
			return err
		}
		counts[status]++
	}

	fmt.Printf("\nsummary: %d synced, %d drifted, %d ok, %d missing, %d disabled\n",
		counts[statusSynced], counts[statusDrifted], counts[statusOK],
		counts[statusMissing], counts[statusDisabled])
	if dryRun && counts[statusDrifted] > 0 {
		syncDriftExit = true
	}
	return nil
}

type syncStatus int

const (
	statusSynced syncStatus = iota
	statusDrifted
	statusMissing
	statusDisabled
	statusOK
	numStatus
)

type syncAgentContext struct {
	byName    map[string]cloudAutomation
	client    *syncClient
	agentsDir string
	dryRun    bool
}

// syncAgent handles one agent: reads its generated agent.md, finds the matching cloud
// automation by name, and patches the prompt when drifted (unless dryRun). It returns
// the outcome status, or an error only for read/transport failures.
func syncAgent(a agent, ctx syncAgentContext) (syncStatus, error) {
	local, err := os.ReadFile(filepath.Join(ctx.agentsDir, a.ID, "agent.md"))
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", filepath.Join(ctx.agentsDir, a.ID, "agent.md"), err)
	}
	remote, found := ctx.byName[a.AutomationName]
	if !found {
		fmt.Printf("MISS   %s — no cloud automation named %q\n", a.ID, a.AutomationName)
		return statusMissing, nil
	}
	if !remote.Enabled {
		fmt.Printf("SKIP   %s — cloud automation %q is disabled\n", a.ID, a.AutomationName)
		return statusDisabled, nil
	}
	if string(local) == remote.Prompt {
		fmt.Printf("OK     %s — %q up to date\n", a.ID, a.AutomationName)
		return statusOK, nil
	}
	if ctx.dryRun {
		fmt.Printf(
			"DRIFT  %s — %q differs (use -sync without -dry-run to apply)\n",
			a.ID,
			a.AutomationName,
		)
		return statusDrifted, nil
	}
	if err := ctx.client.patchPrompt(remote.ID, string(local)); err != nil {
		return 0, fmt.Errorf("patch %s (%s): %w", a.ID, a.AutomationName, err)
	}
	fmt.Printf("SYNCED %s — %q prompt updated\n", a.ID, a.AutomationName)
	return statusSynced, nil
}

// syncDriftExit signals the caller to exit non-zero after a dry run reports drift.
// Tests inspect this instead of calling os.Exit directly.
var syncDriftExit bool
