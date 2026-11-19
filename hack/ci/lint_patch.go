package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	lintCommitID = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	lintSafePath = regexp.MustCompile(
		`^(?:/[A-Za-z0-9_.-][A-Za-z0-9_./-]*|[A-Za-z]:/[A-Za-z0-9_./-]*)$`,
	)
)

type lintPatchRef struct {
	SHA string `json:"sha"`
}

type lintPatchPullRequest struct {
	Base lintPatchRef `json:"base"`
	Head lintPatchRef `json:"head"`
}

type lintPatchEvent struct {
	PullRequest *lintPatchPullRequest `json:"pull_request"`
}

type lintPatchInput struct {
	repo       string
	eventPath  string
	runnerTemp string
}

type lintPatchResult struct {
	base, head, checkout, mergeBase, path, digest string
}

func lintPatchCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		_, _ = fmt.Fprintln(stderr, "lint-patch: unexpected arguments")
		return 2
	}
	repo, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "lint-patch: cannot determine checkout directory")
		return 1
	}
	result, err := generateLintPatch(lintPatchInput{
		repo: repo, eventPath: os.Getenv("GITHUB_EVENT_PATH"),
		runnerTemp: os.Getenv("RUNNER_TEMP"),
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "lint-patch: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(
		stderr,
		"lint-patch: base=%s head=%s checkout=%s merge_base=%s patch_sha256=%s\n",
		result.base,
		result.head,
		result.checkout,
		result.mergeBase,
		result.digest,
	)
	if _, err := fmt.Fprintf(
		stdout,
		"args=--new-from-patch=%s --new=false --new-from-rev= --new-from-merge-base=\n",
		result.path,
	); err != nil {
		_, _ = fmt.Fprintln(stderr, "lint-patch: cannot write action output")
		return 1
	}
	return 0
}

func generateLintPatch(input lintPatchInput) (lintPatchResult, error) {
	var result lintPatchResult
	base, head, err := readLintPatchEvent(input.eventPath)
	if err != nil {
		return result, err
	}
	result.base, result.head = base, head
	checkout, err := validateLintCheckout(input.repo, head)
	if err != nil {
		return result, err
	}
	result.checkout = checkout
	mergeBase, err := uniqueLintMergeBase(input.repo, base, head)
	if err != nil {
		return result, err
	}
	result.mergeBase = mergeBase
	path, digest, err := writeLintPatch(input.repo, input.runnerTemp, mergeBase, head)
	if err != nil {
		return result, err
	}
	current, err := validateLintCheckout(input.repo, head)
	if err != nil || current != checkout {
		return result, errors.New("checkout changed while generating pull request patch")
	}
	result.path, result.digest = path, digest
	return result, nil
}

func readLintPatchEvent(path string) (string, string, error) {
	if path == "" {
		return "", "", errors.New("missing pull request event path")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", "", errors.New("cannot open pull request event")
	}
	defer func() { _ = file.Close() }()
	var event lintPatchEvent
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&event); err != nil || event.PullRequest == nil {
		return "", "", errors.New("invalid pull request event")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", "", errors.New("invalid pull request event")
	}
	base := event.PullRequest.Base.SHA
	head := event.PullRequest.Head.SHA
	if !lintCommitID.MatchString(base) || !lintCommitID.MatchString(head) {
		return "", "", errors.New("pull request base and head must be full SHA1 hashes")
	}
	return strings.ToLower(base), strings.ToLower(head), nil
}

func validateLintCheckout(repo, head string) (string, error) {
	prefix, err := lintGitOutput(repo, "rev-parse", "--show-prefix")
	if err != nil || prefix != "" {
		return "", errors.New("checkout must be the repository root")
	}
	checkout, err := lintGitOutput(repo, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || checkout != head {
		return "", errors.New("checkout does not match pull request head")
	}
	return checkout, nil
}

func uniqueLintMergeBase(repo, base, head string) (string, error) {
	output, err := lintGitOutput(repo, "merge-base", "--all", base, head)
	if err != nil {
		return "", errors.New("cannot compute pull request merge base")
	}
	parts := strings.Fields(output)
	if len(parts) != 1 || !lintCommitID.MatchString(parts[0]) {
		return "", errors.New("pull request must have exactly one merge base")
	}
	return parts[0], nil
}

func lintGitOutput(repo string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func writeLintPatch(repo, temp, mergeBase, head string) (string, string, error) {
	file, path, err := openLintPatch(temp)
	if err != nil {
		return "", "", err
	}
	hash := sha256.New()
	cmd := exec.Command(
		"git",
		"--no-pager",
		"diff",
		"--no-ext-diff",
		"--no-textconv",
		"--no-color",
		"--no-relative",
		"--src-prefix=a/",
		"--dst-prefix=b/",
		"--find-renames",
		"-l0",
		"--unified=3",
		mergeBase,
		head,
		"--",
	)
	cmd.Dir = repo
	cmd.Stdout = io.MultiWriter(file, hash)
	runErr := cmd.Run()
	info, statErr := file.Stat()
	closeErr := file.Close()
	if runErr != nil || statErr != nil || closeErr != nil {
		return "", "", errors.New("cannot generate complete pull request patch")
	}
	if info.Size() == 0 {
		return "", "", errors.New("pull request patch is empty")
	}
	return path, fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func openLintPatch(temp string) (*os.File, string, error) {
	if !filepath.IsAbs(temp) || filepath.Clean(temp) != temp ||
		!lintSafePath.MatchString(filepath.ToSlash(temp)) {
		return nil, "", errors.New("RUNNER_TEMP must be a safe absolute directory")
	}
	dir, err := os.MkdirTemp(temp, "devsy-lint-patch-")
	if err != nil {
		return nil, "", errors.New("cannot create private patch directory")
	}
	path := filepath.Join(dir, "pull-request.patch")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, "", errors.New("cannot create patch file")
	}
	return file, filepath.ToSlash(path), nil
}
