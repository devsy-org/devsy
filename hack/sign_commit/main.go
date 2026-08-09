// Creates a signed Git commit on a branch as the Devsy GitHub App.
//
// Git commits signed locally are unverifiable without a private GPG/SSH key.
// GitHub instead signs commits it creates itself (committer: web-flow). This
// tool authenticates as the app installation and creates the commit through
// the GraphQL createCommitOnBranch mutation, so GitHub signs it and attributes
// the author to devsy-app[bot].
//
// Credentials reuse the gen_github_app_jwt env vars:
//
//   - DEVSY_GITHUB_APP_CLIENT_ID      app client ID (preferred iss claim)
//   - DEVSY_GITHUB_APP_ID             numeric app ID (legacy iss fallback)
//   - DEVSY_GITHUB_APP_PRIVATE_KEY   PEM contents
//   - DEVSY_GITHUB_APP_PRIVATE_KEY_PATH  PEM file path
//
// Usage:
//
//	task github:app:sign-commit -- -m "subject" [-b "body"] [files...]
//
// With no file paths, changed files vs origin/main are committed.
package main

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	envClientID       = "DEVSY_GITHUB_APP_CLIENT_ID"
	envAppID          = "DEVSY_GITHUB_APP_ID"
	envPrivateKey     = "DEVSY_GITHUB_APP_PRIVATE_KEY"
	envPrivateKeyPath = "DEVSY_GITHUB_APP_PRIVATE_KEY_PATH"
	defaultRepo       = "devsy-org/devsy"
	apiBase           = "https://api.github.com"
	maxExpiration     = 10 * time.Minute
	issuedAtSkew      = 60 * time.Second
)

type addition struct {
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

type deletion struct {
	Path string `json:"path"`
}

type fileChange struct {
	Additions []addition `json:"additions,omitempty"`
	Deletions []deletion `json:"deletions,omitempty"`
}

type branchRef struct {
	Repo string `json:"repositoryNameWithOwner"`
	Name string `json:"branchName"`
}

type commitMessage struct {
	Headline string `json:"headline"`
	Body     string `json:"body"`
}

type commitInput struct {
	Branch       branchRef     `json:"branch"`
	Message      commitMessage `json:"message"`
	FileChanges  fileChange    `json:"fileChanges"`
	ExpectedHead string        `json:"expectedHeadOid"`
}

type createCommitVars struct {
	Input commitInput `json:"input"`
}

type options struct {
	message string
	body    string
	repo    string
	branch  string
	all     bool
	token   bool
	paths   []string
}

func main() {
	// Strip a leading "--" separator (e.g. from `task cmd -- -token` or
	// `go run ./hack/sign_commit -- -token`) so flags after it are still parsed
	// by the flag package instead of being treated as positional arguments.
	os.Args = append(os.Args[:1], stripDashDash(os.Args[1:])...)

	message := flag.String("m", "", "commit subject (required unless -token is set)")
	body := flag.String("b", "", "commit body (optional)")
	repo := flag.String("repo", defaultRepo, "owner/name repository")
	branch := flag.String("branch", "", "branch (default: current)")
	all := flag.Bool("all", true, "commit all changed files vs origin/main when no paths given")
	token := flag.Bool("token", false, "print the app installation token and exit (no commit)")
	flag.Parse()

	opts := options{
		message: *message,
		body:    *body,
		repo:    *repo,
		branch:  *branch,
		all:     *all,
		token:   *token,
		paths:   flag.Args(),
	}
	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(o options) error {
	owner, _, ok := strings.Cut(o.repo, "/")
	if !ok {
		return fmt.Errorf("invalid repo %q, want owner/name", o.repo)
	}

	token, err := appToken(owner)
	if err != nil {
		return err
	}
	if o.token {
		fmt.Println(token)
		return nil
	}
	return commitFiles(o, token)
}

func commitFiles(o options, token string) error {
	if o.message == "" {
		return fmt.Errorf("-m commit subject is required (or pass -token to print the app token)")
	}

	branch := o.branch
	if branch == "" {
		var err error
		branch, err = currentBranch()
		if err != nil {
			return fmt.Errorf("detect branch: %w", err)
		}
	}

	paths, err := resolvePaths(o.all, o.paths)
	if err != nil {
		return err
	}

	head, err := refSHA(token, o.repo, branch)
	if err != nil {
		return fmt.Errorf("read branch head: %w", err)
	}

	adds, dels, err := fileChanges(paths, os.ReadFile)
	if err != nil {
		return err
	}

	vars := commitVars(o, branch, head, fileChange{Additions: adds, Deletions: dels})
	return publishCommit(token, o.repo, vars)
}

func publishCommit(token, repo string, vars createCommitVars) error {
	sha, err := createCommit(token, vars)
	if err != nil {
		return fmt.Errorf("create commit: %w", err)
	}
	verified, err := commitVerified(token, repo, sha)
	if err != nil {
		return fmt.Errorf("verify commit: %w", err)
	}
	fmt.Printf("%s verified=%v\n", sha, verified)
	return nil
}

func appToken(owner string) (string, error) {
	clientID := os.Getenv(envClientID)
	if clientID == "" {
		clientID = os.Getenv(envAppID)
	}
	if clientID == "" {
		return "", fmt.Errorf("client id missing: set %s or %s", envClientID, envAppID)
	}
	key, err := loadPrivateKey(os.Getenv(envPrivateKeyPath), os.Getenv(envPrivateKey))
	if err != nil {
		return "", fmt.Errorf("load private key: %w", err)
	}
	jwtToken, err := generateJWT(clientID, key, time.Now())
	if err != nil {
		return "", fmt.Errorf("generate jwt: %w", err)
	}
	token, err := installationToken(jwtToken, owner)
	if err != nil {
		return "", fmt.Errorf("installation token: %w", err)
	}
	return token, nil
}

func resolvePaths(all bool, paths []string) ([]string, error) {
	if len(paths) > 0 {
		return paths, nil
	}
	if !all {
		return nil, fmt.Errorf("no file paths given")
	}
	files, err := changedFiles()
	if err != nil {
		return nil, fmt.Errorf("list changed files: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no changed files to commit")
	}
	return files, nil
}

func commitVars(o options, branch, head string, changes fileChange) createCommitVars {
	headline, bodyText := splitMessage(o.message, o.body)
	return createCommitVars{
		Input: commitInput{
			Branch:       branchRef{Repo: o.repo, Name: "refs/heads/" + branch},
			Message:      commitMessage{Headline: headline, Body: bodyText},
			FileChanges:  changes,
			ExpectedHead: head,
		},
	}
}

func splitMessage(message, body string) (string, string) {
	if body != "" {
		return sanitizeSecrets(stripCoAuthored(message)), sanitizeSecrets(stripCoAuthored(body))
	}
	headline, rest, _ := strings.Cut(message, "\n")
	return headline, sanitizeSecrets(stripCoAuthored(strings.TrimSpace(rest)))
}

// stripCoAuthored removes Co-authored-by trailers so commits always have a
// single author (the Devsy GitHub App). The trailer may appear anywhere in
// the body; matching is case-insensitive and tolerates surrounding whitespace.
func stripCoAuthored(s string) string {
	var kept []string
	for line := range strings.SplitSeq(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trimmed), "co-authored-by:") {
			continue
		}
		kept = append(kept, line)
	}
	out := strings.Join(kept, "\n")
	return strings.TrimSpace(out)
}

var (
	// ghPat matches GitHub installation tokens (ghs_), OAuth tokens (gho_),
	// user-to-server tokens (ghu_), and personal access tokens (ghp_/github_pat_).
	ghPat = regexp.MustCompile(
		`gh[sou]_[A-Za-z0-9]{36,}|ghp_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{82}`,
	)
	// jwtPat matches RS256/ES256 JWT tokens (three dot-separated base64url segments).
	jwtPat = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)
)

// sanitizeSecrets replaces GitHub tokens and JWTs in s with "[REDACTED]" so
// that secrets accidentally interpolated into commit messages (e.g. via shell
// command substitution) are never persisted to git history.
func sanitizeSecrets(s string) string {
	s = ghPat.ReplaceAllString(s, "[REDACTED]")
	s = jwtPat.ReplaceAllString(s, "[REDACTED]")
	return s
}

func fileChanges(
	paths []string, read func(string) ([]byte, error),
) ([]addition, []deletion, error) {
	var adds []addition
	var dels []deletion
	for _, p := range paths {
		b, err := read(p)
		if err != nil {
			if os.IsNotExist(err) {
				dels = append(dels, deletion{Path: p})
				continue
			}
			return nil, nil, fmt.Errorf("read %s: %w", p, err)
		}
		adds = append(adds, addition{Path: p, Contents: base64.StdEncoding.EncodeToString(b)})
	}
	return adds, dels, nil
}

func generateJWT(clientID string, key *rsa.PrivateKey, now time.Time) (string, error) {
	if clientID == "" {
		return "", fmt.Errorf("client id missing: set %s or %s", envClientID, envAppID)
	}
	claims := jwt.RegisteredClaims{
		Issuer:    clientID,
		IssuedAt:  jwt.NewNumericDate(now.Add(-issuedAtSkew)),
		ExpiresAt: jwt.NewNumericDate(now.Add(maxExpiration)),
	}
	return jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
}

func loadPrivateKey(path, contents string) (*rsa.PrivateKey, error) {
	switch {
	case contents != "":
		return jwt.ParseRSAPrivateKeyFromPEM([]byte(contents))
	case path != "":
		b, err := os.ReadFile(path) // #nosec G304 -- path from a trusted env var
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		return jwt.ParseRSAPrivateKeyFromPEM(b)
	default:
		return nil, fmt.Errorf("no private key: set %s or %s", envPrivateKey, envPrivateKeyPath)
	}
}

func installationURL(owner string) string {
	return apiBase + "/orgs/" + owner + "/installation"
}

func installationToken(jwtToken, owner string) (string, error) {
	inst, err := ghGet(jwtToken, installationURL(owner))
	if err != nil {
		return "", err
	}
	id, ok := inst["id"].(float64)
	if !ok {
		return "", fmt.Errorf("installation id not found for org %s", owner)
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", apiBase, int64(id))
	body, err := ghPost(jwtToken, url, nil)
	if err != nil {
		return "", err
	}
	token, ok := body["token"].(string)
	if !ok {
		return "", fmt.Errorf("installation token missing in response")
	}
	return token, nil
}

func refSHA(token, repo, branch string) (string, error) {
	body, err := ghGet(token, fmt.Sprintf("%s/repos/%s/git/refs/heads/%s", apiBase, repo, branch))
	if err != nil {
		return "", err
	}
	obj, ok := body["object"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("ref object missing")
	}
	sha, ok := obj["sha"].(string)
	if !ok {
		return "", fmt.Errorf("ref sha missing")
	}
	return sha, nil
}

func commitVerified(token, repo, sha string) (bool, error) {
	body, err := ghGet(token, fmt.Sprintf("%s/repos/%s/commits/%s", apiBase, repo, sha))
	if err != nil {
		return false, err
	}
	commit, ok := body["commit"].(map[string]any)
	if !ok {
		return false, fmt.Errorf("commit missing")
	}
	ver, ok := commit["verification"].(map[string]any)
	if !ok {
		return false, nil
	}
	return ver["verified"] == true, nil
}

func createCommit(token string, vars createCommitVars) (string, error) {
	query := `mutation($input: CreateCommitOnBranchInput!) {
  createCommitOnBranch(input: $input) { commit { oid } }
}`
	payload := map[string]any{"query": query, "variables": vars}
	body, err := ghPost(token, apiBase+"/graphql", payload)
	if err != nil {
		return "", err
	}
	data, ok := body["data"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("graphql error: %v", body["errors"])
	}
	cc, ok := data["createCommitOnBranch"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("createCommitOnBranch missing")
	}
	commit, ok := cc["commit"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("commit missing")
	}
	sha, ok := commit["oid"].(string)
	if !ok {
		return "", fmt.Errorf("commit oid missing")
	}
	return sha, nil
}

func ghGet(token, url string) (map[string]any, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	return do(req)
}

func ghPost(token, url string, payload any) (map[string]any, error) {
	var r io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		r = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(http.MethodPost, url, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return do(req)
}

func do(req *http.Request) (map[string]any, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return out, fmt.Errorf("%s %s", resp.Status, urlOrErr(out))
	}
	return out, nil
}

func urlOrErr(body map[string]any) string {
	if m, ok := body["message"].(string); ok {
		return m
	}
	return ""
}

func currentBranch() (string, error) {
	return gitOutput("rev-parse", "--abbrev-ref", "HEAD")
}

func changedFiles() ([]string, error) {
	base, err := gitOutput("merge-base", "HEAD", "origin/main")
	if err != nil {
		return nil, err
	}
	out, err := gitOutput("diff", "--name-only", base, "HEAD")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

func gitOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	stdout, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(stdout)), nil
}

func splitLines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// stripDashDash removes a single leading "--" separator from args so that
// flags after it (e.g. from `task cmd -- -token` or `go run ./tool -- -token`)
// are parsed by the flag package instead of being treated as positional args.
func stripDashDash(args []string) []string {
	if len(args) > 0 && args[0] == "--" {
		return args[1:]
	}
	return args
}
