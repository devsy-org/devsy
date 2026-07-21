package git

import (
	"context"
	"errors"
	"testing"

	"gotest.tools/assert"
	"gotest.tools/assert/cmp"
)

// Shared literals used across Repo tests.
const (
	testRepoURL   = "git@host:org/repo.git"
	testGitHubURL = "https://github.com/org/repo.git"
	testGitLabURL = "git@gitlab.com:org/repo.git"
	testPRRef     = "pull/996/head"
	testPRLocal   = "PR996"
	testPRRefSpec = testPRRef + ":" + testPRLocal
	testMRLocal   = "MR7"
	testMRRefSpec = "merge-requests/7/head:" + testMRLocal
	testCommit    = "abc123"
	subFetch      = "fetch"
	subSwitch     = "switch"
	originRemote  = "origin"
	testTarget    = "/tmp/target"
)

// fakeRunner records the invocations it receives and returns canned output,
// letting git operations be tested without a real repository.
type fakeRunner struct {
	calls  []RunOptions
	stdout []byte
	err    error
	// errUntil makes the first errUntil calls fail, simulating a missing
	// remote ref that triggers the CheckoutPR fallback.
	errUntil int
}

func (f *fakeRunner) Run(_ context.Context, opts RunOptions) (RunResult, error) {
	f.calls = append(f.calls, opts)
	if len(f.calls) <= f.errUntil {
		return RunResult{}, &CommandError{
			Args:     opts.Args,
			ExitCode: 128,
			Stderr:   "fatal: couldn't find remote ref " + opts.Args[len(opts.Args)-1],
			Err:      errors.New("exit status 128"),
		}
	}
	return RunResult{Stdout: f.stdout}, f.err
}

func (f *fakeRunner) lastArgs() []string {
	if len(f.calls) == 0 {
		return nil
	}
	return f.calls[len(f.calls)-1].Args
}

func TestRepoFetch(t *testing.T) {
	fake := &fakeRunner{}
	repo := At("/tmp/repo", WithRunner(fake))

	assert.NilError(t, repo.Fetch(context.Background(), testPRRefSpec))
	assert.DeepEqual(t, []string{subFetch, originRemote, testPRRefSpec}, fake.lastArgs())
	assert.Equal(t, "/tmp/repo", fake.calls[0].Dir)
}

func TestRepoReset(t *testing.T) {
	fake := &fakeRunner{}
	repo := At("/tmp/repo", WithRunner(fake))

	assert.NilError(t, repo.Reset(context.Background(), testCommit, ResetHard))
	assert.DeepEqual(t, []string{"reset", "--hard", testCommit}, fake.lastArgs())
}

func TestRepoCheckoutPR(t *testing.T) {
	fake := &fakeRunner{}
	repo := At("/tmp/repo", WithRunner(fake))

	err := repo.CheckoutPR(context.Background(), testGitHubURL, testPRRef)
	assert.NilError(t, err)
	// Expect a fetch of the PR ref into a local branch, then a switch to it.
	assert.DeepEqual(t, []string{subFetch, originRemote, testPRRefSpec}, fake.calls[0].Args)
	assert.DeepEqual(t, []string{subSwitch, testPRLocal}, fake.calls[1].Args)
}

func TestRepoCheckoutPRGitLab(t *testing.T) {
	fake := &fakeRunner{}
	repo := At("/tmp/repo", WithRunner(fake))

	err := repo.CheckoutPR(context.Background(), testGitLabURL, "merge-requests/7/head")
	assert.NilError(t, err)
	assert.DeepEqual(t, []string{subFetch, originRemote, testMRRefSpec}, fake.calls[0].Args)
	assert.DeepEqual(t, []string{subSwitch, testMRLocal}, fake.calls[1].Args)
}

// A GitLab MR requested with a GitHub-style ref still resolves: host detection
// from the URL picks GitLab and rewrites the refspec to merge-requests/N/head.
func TestRepoCheckoutPRGitLabFromGitHubStyleRef(t *testing.T) {
	fake := &fakeRunner{}
	repo := At("/tmp/repo", WithRunner(fake))

	err := repo.CheckoutPR(context.Background(), testGitLabURL, "pull/7/head")
	assert.NilError(t, err)
	assert.DeepEqual(t, []string{subFetch, originRemote, testMRRefSpec}, fake.calls[0].Args)
	assert.DeepEqual(t, []string{subSwitch, testMRLocal}, fake.calls[1].Args)
}

// When the detected host's ref is missing (undetectable self-hosted instance),
// the fetch falls back to the other known convention.
func TestRepoCheckoutPRFallback(t *testing.T) {
	fake := &fakeRunner{errUntil: 1}
	repo := At("/tmp/repo", WithRunner(fake))

	// URL detection yields GitHub (default); its fetch fails, so the checkout
	// falls back to the GitLab convention.
	err := repo.CheckoutPR(
		context.Background(),
		"https://git.internal.example/org/repo.git",
		"pull/7/head",
	)
	assert.NilError(t, err)
	assert.DeepEqual(t, []string{subFetch, originRemote, "pull/7/head:PR7"}, fake.calls[0].Args)
	assert.DeepEqual(t, []string{subFetch, originRemote, testMRRefSpec}, fake.calls[1].Args)
	assert.DeepEqual(t, []string{subSwitch, testMRLocal}, fake.calls[2].Args)
}

// A non-ref fetch failure (auth, network, …) must surface immediately without
// being masked by the alternate-provider fallback.
func TestRepoCheckoutPRNonRefErrorNoFallback(t *testing.T) {
	authErr := &CommandError{
		Args:     []string{subFetch},
		ExitCode: 128,
		Stderr:   "fatal: Authentication failed for 'https://host/org/repo.git'",
		Err:      errors.New("exit status 128"),
	}
	fake := &fakeRunner{err: authErr}
	repo := At("/tmp/repo", WithRunner(fake))

	err := repo.CheckoutPR(context.Background(), testGitHubURL, testPRRef)
	assert.ErrorContains(t, err, "Authentication failed")
	// Only the first candidate is attempted; the auth error is not retried.
	assert.Equal(t, 1, len(fake.calls))
}

func TestRepoLsRemote(t *testing.T) {
	fake := &fakeRunner{}
	repo := At("", WithRunner(fake))

	assert.NilError(t, repo.LsRemote(context.Background(), testRepoURL))
	assert.DeepEqual(t, []string{"ls-remote", "--quiet", testRepoURL}, fake.lastArgs())
}

func TestRepoLsTree(t *testing.T) {
	fake := &fakeRunner{stdout: []byte("a/b.txt\n\n  c.txt \nd/e/f.go\n")}
	repo := At("/tmp/repo", WithRunner(fake))

	paths, err := repo.LsTree(context.Background(), "HEAD")
	assert.NilError(t, err)
	assert.DeepEqual(
		t,
		[]string{"ls-tree", "-r", "--full-name", "--name-only", "HEAD"},
		fake.lastArgs(),
	)
	// Blank lines dropped, surrounding whitespace trimmed.
	assert.DeepEqual(t, []string{"a/b.txt", "c.txt", "d/e/f.go"}, paths)
}

func TestRepoCloneArgsThroughRunner(t *testing.T) {
	fake := &fakeRunner{}
	repo := At(testTarget, WithRunner(fake))

	err := repo.Clone(context.Background(), testRepoURL,
		WithCloneStrategy(ShallowCloneStrategy),
		WithBranch(testBranch),
	)
	assert.NilError(t, err)
	assert.DeepEqual(t, []string{
		subClone, "--depth=1", flagBranch, testBranch,
		testRepoURL, testTarget, flagProgress,
	}, fake.lastArgs())
}

func TestRepoEnvThreadedToRunner(t *testing.T) {
	fake := &fakeRunner{}
	repo := At("/tmp/repo", WithRunner(fake), WithEnv([]string{"GIT_TERMINAL_PROMPT=0"}))

	assert.NilError(t, repo.Fetch(context.Background(), "main"))
	assert.Assert(t, cmp.Contains(fake.calls[0].Env, "GIT_TERMINAL_PROMPT=0"))
}

func TestRepoEnvOptionsCompose(t *testing.T) {
	fake := &fakeRunner{}
	// Both env-setting options must contribute; neither should overwrite the other.
	repo := At("/tmp/repo",
		WithRunner(fake),
		WithStrictHostKeyChecking(false),
		WithEnv([]string{"CUSTOM=1"}),
	)

	assert.NilError(t, repo.Fetch(context.Background(), "main"))
	env := fake.calls[0].Env
	assert.Assert(t, cmp.Contains(env, "GIT_TERMINAL_PROMPT=0")) // from strict-host-key option
	assert.Assert(t, cmp.Contains(env, "CUSTOM=1"))              // from WithEnv
}

func TestRepoCloneFromInfoBranch(t *testing.T) {
	fake := &fakeRunner{}
	repo := At(testTarget, WithRunner(fake))
	info := &GitInfo{Repository: testRepoURL, Branch: testBranch}

	assert.NilError(t, repo.CloneFromInfo(context.Background(), info, ""))
	// Branch becomes a clone flag; no separate checkout call.
	assert.DeepEqual(t, []string{
		subClone, flagBranch, testBranch,
		testRepoURL, testTarget, flagProgress,
	}, fake.calls[0].Args)
}

func TestRepoCloneFromInfoCommit(t *testing.T) {
	fake := &fakeRunner{}
	repo := At(testTarget, WithRunner(fake))
	info := &GitInfo{Repository: testRepoURL, Commit: testCommit}

	assert.NilError(t, repo.CloneFromInfo(context.Background(), info, ""))
	// clone, then hard reset to the commit.
	assert.Equal(t, subClone, fake.calls[0].Args[0])
	assert.DeepEqual(t, []string{"reset", "--hard", testCommit}, fake.calls[1].Args)
}

func TestRepoCloneFromInfoPR(t *testing.T) {
	fake := &fakeRunner{}
	repo := At(testTarget, WithRunner(fake))
	info := &GitInfo{Repository: testRepoURL, PR: testPRRef}

	assert.NilError(t, repo.CloneFromInfo(context.Background(), info, ""))
	assert.Equal(t, subClone, fake.calls[0].Args[0])
	assert.DeepEqual(t, []string{subFetch, originRemote, testPRRefSpec}, fake.calls[1].Args)
	assert.DeepEqual(t, []string{subSwitch, testPRLocal}, fake.calls[2].Args)
}

func TestRepoCloneFromInfoHelper(t *testing.T) {
	fake := &fakeRunner{}
	repo := At(testTarget, WithRunner(fake))
	info := &GitInfo{Repository: "https://host/org/repo.git"}

	assert.NilError(t, repo.CloneFromInfo(context.Background(), info, "store"))
	assert.DeepEqual(t, []string{
		subClone, flagConfig, "credential.helper=store",
		"https://host/org/repo.git", testTarget, flagProgress,
	}, fake.calls[0].Args)
}
