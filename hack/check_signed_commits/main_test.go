package main

import (
	"errors"
	"strings"
	"testing"
)

const (
	cmdGit          = "git"
	cmdRevList      = "rev-list"
	cmdCatFile      = "cat-file"
	cmdVerifyCommit = "verify-commit"
	envToRef        = "PRE_COMMIT_TO_REF"
	envFromRef      = "PRE_COMMIT_FROM_REF"
	zeroHash        = "0000000000000000000000000000000000000000"
	dummySignature  = "tree 123\ngpgsig -----BEGIN SSH SIGNATURE-----\n\nmsg"
)

func TestIsAllZeros(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"", false},
		{"0", true},
		{zeroHash, true},
		{"0000000000000000000000000000000000000001", false},
		{"a000", false},
	}

	for _, tt := range tests {
		if got := isAllZeros(tt.input); got != tt.want {
			t.Errorf("isAllZeros(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestHasSignatureHeaderPGPAndSSH(t *testing.T) {
	pgpCommit := []byte(
		"tree 1\nauthor T\ngpgsig -----BEGIN PGP SIGNATURE-----\n abc\n -----END PGP SIGNATURE-----\n\nmsg",
	)
	sshCommit := []byte(
		"tree 2\nauthor T\ngpgsig -----BEGIN SSH SIGNATURE-----\n abc\n -----END SSH SIGNATURE-----\n\nmsg",
	)

	if !hasSignatureHeader(pgpCommit) {
		t.Errorf("expected pgpCommit to have signature header")
	}
	if !hasSignatureHeader(sshCommit) {
		t.Errorf("expected sshCommit to have signature header")
	}
}

func TestHasSignatureHeaderOther(t *testing.T) {
	sha256Commit := []byte(
		"tree 3\ngpgsig-sha256 -----BEGIN PGP SIGNATURE-----\n abc\n\nmsg",
	)
	unsignedCommit := []byte("tree 4\nauthor T\n\nmsg")
	bodyOnlyGpgsig := []byte("tree 5\nauthor T\n\ngpgsig in commit body")

	if !hasSignatureHeader(sha256Commit) {
		t.Errorf("expected sha256Commit to have signature header")
	}
	if hasSignatureHeader(unsignedCommit) {
		t.Errorf("expected unsignedCommit to not have signature header")
	}
	if hasSignatureHeader(bodyOnlyGpgsig) {
		t.Errorf("expected bodyOnlyGpgsig to not have signature header")
	}
}

func TestIsCommitSignedVerifySuccess(t *testing.T) {
	runner := func(name string, args ...string) ([]byte, error) {
		if name == cmdGit && len(args) >= 2 && args[0] == cmdVerifyCommit {
			return []byte("Good signature"), nil
		}
		return nil, errors.New("unexpected command")
	}
	if !isCommitSigned("abc", runner) {
		t.Errorf("expected commit to be signed when verify-commit succeeds")
	}
}

func TestIsCommitSignedCatFileFallback(t *testing.T) {
	runner := func(name string, args ...string) ([]byte, error) {
		if name == cmdGit && len(args) >= 2 && args[0] == cmdVerifyCommit {
			return nil, errors.New("verification failed")
		}
		if name == cmdGit && len(args) >= 3 && args[0] == cmdCatFile {
			return []byte(dummySignature), nil
		}
		return nil, errors.New("unexpected command")
	}
	if !isCommitSigned("abc", runner) {
		t.Errorf("expected commit to be signed when cat-file has gpgsig")
	}
}

func TestIsCommitSignedUnsigned(t *testing.T) {
	runner := func(name string, args ...string) ([]byte, error) {
		if name == cmdGit && len(args) >= 2 && args[0] == cmdVerifyCommit {
			return nil, errors.New("verification failed")
		}
		if name == cmdGit && len(args) >= 3 && args[0] == cmdCatFile {
			return []byte("tree 123\nauthor Test\n\nmsg"), nil
		}
		return nil, errors.New("unexpected command")
	}
	if isCommitSigned("abc", runner) {
		t.Errorf("expected commit to not be signed")
	}
}

func TestCollectCommitsFromEnvBranchDeletion(t *testing.T) {
	env := map[string]string{envToRef: zeroHash}
	commits, handled, err := collectCommitsFromEnv(
		func(k string) string { return env[k] },
		nil,
	)
	if err != nil || !handled || len(commits) != 0 {
		t.Errorf(
			"expected branch deletion handled with 0 commits, got handled=%v, commits=%v, err=%v",
			handled,
			commits,
			err,
		)
	}
}

func TestCollectCommitsFromEnvExistingRange(t *testing.T) {
	env := map[string]string{
		envFromRef: "base123",
		envToRef:   "head456",
	}
	runner := func(_ string, args ...string) ([]byte, error) {
		if args[0] == cmdRevList && args[1] == "base123..head456" {
			return []byte("commit1\ncommit2\n"), nil
		}
		return nil, errors.New("unexpected command")
	}
	commits, handled, err := collectCommitsFromEnv(
		func(k string) string { return env[k] },
		runner,
	)
	if err != nil || !handled || len(commits) != 2 {
		t.Errorf(
			"expected 2 commits, got handled=%v, len=%d, err=%v",
			handled,
			len(commits),
			err,
		)
	}
}

func TestCollectCommitsFromStdin(t *testing.T) {
	stdinData := `refs/heads/main 1111 refs/heads/main 2222
refs/heads/del 0000000000000000000000000000000000000000 refs/heads/del 3333
refs/heads/new 4444 refs/heads/new 0000000000000000000000000000000000000000
`
	runner := func(_ string, args ...string) ([]byte, error) {
		if args[0] == cmdRevList && args[1] == "2222..1111" {
			return []byte("c1\n"), nil
		}
		if args[0] == cmdRevList && args[1] == "4444" {
			return []byte("c2\n"), nil
		}
		return nil, errors.New("unexpected command")
	}

	commits, hasInput, err := collectCommitsFromStdin(
		strings.NewReader(stdinData),
		runner,
	)
	if err != nil || !hasInput || len(commits) != 2 {
		t.Fatalf(
			"expected 2 commits from stdin, got %v, hasInput=%v, err=%v",
			commits,
			hasInput,
			err,
		)
	}
}

func TestRunUnsigned(t *testing.T) {
	env := map[string]string{
		envFromRef: "base",
		envToRef:   "head",
	}
	runner := func(_ string, args ...string) ([]byte, error) {
		switch args[0] {
		case cmdRevList:
			return []byte("c_unsigned\n"), nil
		case cmdVerifyCommit:
			return nil, errors.New("unverified")
		case cmdCatFile:
			return []byte("tree 1\nauthor A\n\nmsg"), nil
		case "log":
			return []byte("c_unsigned\x00feat: test\x00Author <a@b.com>"), nil
		}
		return nil, errors.New("unexpected")
	}

	ctx := runContext{
		getenv:      func(k string) string { return env[k] },
		runner:      runner,
		isStdinPipe: false,
	}
	err := run(ctx)
	if err == nil {
		t.Fatalf("expected error for unsigned commit")
	}
	if !strings.Contains(err.Error(), "Unsigned commit(s) detected") {
		t.Errorf("expected error message, got: %v", err)
	}
}

func TestRunSigned(t *testing.T) {
	env := map[string]string{
		envFromRef: "base",
		envToRef:   "head",
	}
	runner := func(_ string, args ...string) ([]byte, error) {
		if args[0] == cmdRevList {
			return []byte("c_signed\n"), nil
		}
		if args[0] == cmdVerifyCommit {
			return []byte("Good signature"), nil
		}
		return nil, errors.New("unexpected")
	}

	ctx := runContext{
		getenv:      func(k string) string { return env[k] },
		runner:      runner,
		isStdinPipe: false,
	}
	if err := run(ctx); err != nil {
		t.Fatalf("expected success for signed commit, got: %v", err)
	}
}
