package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type commandRunner func(name string, args ...string) ([]byte, error)

type runContext struct {
	getenv      func(string) string
	stdin       io.Reader
	isStdinPipe bool
	runner      commandRunner
}

type commitInfo struct {
	sha       string
	shortHash string
	subject   string
	author    string
}

func realRunner(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	return cmd.Output()
}

func isAllZeros(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c != '0' {
			return false
		}
	}
	return true
}

func splitLines(b []byte) []string {
	var lines []string
	scanner := bufio.NewScanner(bytes.NewReader(b))
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text != "" {
			lines = append(lines, text)
		}
	}
	return lines
}

func hasSignatureHeader(commitData []byte) bool {
	header, _, _ := bytes.Cut(commitData, []byte("\n\n"))
	for line := range bytes.SplitSeq(header, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("gpgsig ")) ||
			bytes.HasPrefix(line, []byte("gpgsig-sha256 ")) {
			return true
		}
	}
	return false
}

func isCommitSigned(commit string, runner commandRunner) bool {
	if _, err := runner("git", "verify-commit", commit); err == nil {
		return true
	}
	out, err := runner("git", "cat-file", "commit", commit)
	if err != nil {
		return false
	}
	return hasSignatureHeader(out)
}

func commitsFromNewBranch(
	toRef, remoteName string,
	runner commandRunner,
) ([]string, error) {
	if remoteName == "" {
		remoteName = "origin"
	}
	out, err := runner(
		"git",
		"rev-list",
		toRef,
		"--not",
		"--remotes="+remoteName,
	)
	if err == nil && len(bytes.TrimSpace(out)) > 0 {
		return splitLines(out), nil
	}
	out, err = runner("git", "rev-list", toRef, "--not", "--remotes")
	if err == nil && len(bytes.TrimSpace(out)) > 0 {
		return splitLines(out), nil
	}
	out, err = runner("git", "rev-list", "-n", "1", toRef)
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

func collectCommitsFromEnv(
	getenv func(string) string,
	runner commandRunner,
) ([]string, bool, error) {
	toRef := getenv("PRE_COMMIT_TO_REF")
	if toRef == "" {
		return nil, false, nil
	}

	// Remote branch deletion
	if isAllZeros(toRef) {
		return nil, true, nil
	}

	fromRef := getenv("PRE_COMMIT_FROM_REF")
	if fromRef != "" && !isAllZeros(fromRef) {
		out, err := runner("git", "rev-list", fromRef+".."+toRef)
		if err != nil {
			return nil, true, err
		}
		return splitLines(out), true, nil
	}

	commits, err := commitsFromNewBranch(
		toRef,
		getenv("PRE_COMMIT_REMOTE_NAME"),
		runner,
	)
	return commits, true, err
}

func commitsForSHA(
	localSHA, remoteSHA string,
	runner commandRunner,
) ([]string, error) {
	if isAllZeros(remoteSHA) || remoteSHA == "" {
		out, err := runner("git", "rev-list", localSHA, "--not", "--remotes")
		if err == nil && len(bytes.TrimSpace(out)) > 0 {
			return splitLines(out), nil
		}
		out, err = runner("git", "rev-list", "-n", "1", localSHA)
		if err != nil {
			return nil, err
		}
		return splitLines(out), nil
	}

	out, err := runner("git", "rev-list", remoteSHA+".."+localSHA)
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

func parseStdinLine(line string, runner commandRunner) ([]string, error) {
	fields := strings.Fields(line)
	if len(fields) < 4 || isAllZeros(fields[1]) {
		return nil, nil
	}
	return commitsForSHA(fields[1], fields[3], runner)
}

func collectCommitsFromStdin(
	r io.Reader,
	runner commandRunner,
) ([]string, bool, error) {
	var commits []string
	scanner := bufio.NewScanner(r)
	hasInput := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		hasInput = true
		c, err := parseStdinLine(line, runner)
		if err != nil {
			return nil, true, err
		}
		commits = append(commits, c...)
	}

	return commits, hasInput, scanner.Err()
}

func fallbackCommits(runner commandRunner) ([]string, error) {
	out, err := runner("git", "rev-list", "HEAD", "--not", "--remotes")
	if err == nil && len(bytes.TrimSpace(out)) > 0 {
		return splitLines(out), nil
	}
	out, err = runner("git", "rev-list", "-n", "1", "HEAD")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

func collectCommits(ctx runContext) ([]string, error) {
	commits, handled, err := collectCommitsFromEnv(ctx.getenv, ctx.runner)
	if handled {
		return commits, err
	}

	if ctx.isStdinPipe {
		stdinCommits, hasInput, scanErr := collectCommitsFromStdin(
			ctx.stdin,
			ctx.runner,
		)
		if scanErr != nil {
			return nil, scanErr
		}
		if hasInput {
			return stdinCommits, nil
		}
	}

	return fallbackCommits(ctx.runner)
}

func getCommitInfo(commit string, runner commandRunner) commitInfo {
	info := commitInfo{sha: commit, shortHash: commit}
	out, err := runner(
		"git",
		"log",
		"-1",
		"--format=%h%x00%s%x00%an <%ae>",
		commit,
	)
	if err == nil {
		parts := strings.Split(string(bytes.TrimSpace(out)), "\x00")
		if len(parts) >= 3 {
			info.shortHash = parts[0]
			info.subject = parts[1]
			info.author = parts[2]
		}
	}
	return info
}

func findUnsignedCommits(
	commits []string,
	runner commandRunner,
) []commitInfo {
	seen := make(map[string]bool, len(commits))
	var unsigned []commitInfo

	for _, commit := range commits {
		if seen[commit] {
			continue
		}
		seen[commit] = true

		if !isCommitSigned(commit, runner) {
			unsigned = append(unsigned, getCommitInfo(commit, runner))
		}
	}
	return unsigned
}

func formatUnsignedError(unsigned []commitInfo) string {
	var b strings.Builder
	b.WriteString(
		"ERROR: Unsigned commit(s) detected. All commits pushed to the repository must be cryptographically signed.\n\n",
	)
	b.WriteString("Unsigned commits:\n")
	for _, info := range unsigned {
		fmt.Fprintf(
			&b,
			"  - %s: %s (%s)\n",
			info.shortHash,
			info.subject,
			info.author,
		)
	}
	b.WriteString("\nTo sign your commits before pushing:\n")
	b.WriteString("  - Configure automatic signing in git:\n")
	b.WriteString("      git config commit.gpgsign true\n")
	b.WriteString("  - To sign your latest commit:\n")
	b.WriteString("      git commit --amend --no-edit -S\n")
	b.WriteString("  - To sign multiple previous commits:\n")
	b.WriteString(
		"      git rebase --exec 'git commit --amend --no-edit -S' <base-commit>",
	)
	return b.String()
}

func run(ctx runContext) error {
	commits, err := collectCommits(ctx)
	if err != nil {
		return fmt.Errorf("determine commits to check: %w", err)
	}

	if len(commits) == 0 {
		return nil
	}

	unsigned := findUnsignedCommits(commits, ctx.runner)
	if len(unsigned) > 0 {
		return errors.New(formatUnsignedError(unsigned))
	}

	fmt.Println("All commits are signed.")
	return nil
}

func main() {
	stat, err := os.Stdin.Stat()
	isPipe := err == nil && (stat.Mode()&os.ModeCharDevice) == 0

	ctx := runContext{
		getenv:      os.Getenv,
		stdin:       os.Stdin,
		isStdinPipe: isPipe,
		runner:      realRunner,
	}

	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
