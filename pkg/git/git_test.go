package git

import (
	"testing"

	"gotest.tools/assert"
	"gotest.tools/assert/cmp"
)

const (
	repoDevsyHTTPS        = "https://github.com/devsy-org/devsy.git"
	repoDevsyNoProtoSlash = "https://github.com/devsy-org/devsy-without-protocol-with-slash.git"
	repoFileProject       = "file:///workspace/projects/project"
	branchTestBranch      = "test-branch"
	testCommitSHA         = "905ffb0"
	testSubpathVal        = "/test/path"
)

type testCaseNormalizeRepository struct {
	in                  string
	expectedPRReference string
	expectedRepo        string
	expectedBranch      string
	expectedCommit      string
	expectedSubpath     string
}

type testCaseGetBranchNameForPR struct {
	in             string
	expectedBranch string
}

var normalizeRepositoryCases = []testCaseNormalizeRepository{
	{
		in:                  "ssh://github.com/devsy-org/devsy.git",
		expectedRepo:        "ssh://github.com/devsy-org/devsy.git",
		expectedPRReference: "",
		expectedBranch:      "",
		expectedCommit:      "",
		expectedSubpath:     "",
	},
	{
		in:                  "ssh://git@github.com/devsy-org/devsy.git",
		expectedRepo:        "ssh://git@github.com/devsy-org/devsy.git",
		expectedPRReference: "",
		expectedBranch:      "",
		expectedCommit:      "",
		expectedSubpath:     "",
	},
	{
		in:                  "git@github.com:devsy-org/devsy-without-branch.git",
		expectedRepo:        "git@github.com:devsy-org/devsy-without-branch.git",
		expectedPRReference: "",
		expectedBranch:      "",
		expectedCommit:      "",
		expectedSubpath:     "",
	},
	{
		in:                  repoDevsyHTTPS,
		expectedRepo:        repoDevsyHTTPS,
		expectedPRReference: "",
		expectedBranch:      "",
		expectedCommit:      "",
		expectedSubpath:     "",
	},
	{
		in:                  "github.com/devsy-org/devsy.git",
		expectedRepo:        repoDevsyHTTPS,
		expectedPRReference: "",
		expectedBranch:      "",
		expectedCommit:      "",
		expectedSubpath:     "",
	},
	{
		in:                  "github.com/devsy-org/devsy.git@test-branch",
		expectedRepo:        repoDevsyHTTPS,
		expectedPRReference: "",
		expectedBranch:      branchTestBranch,
		expectedCommit:      "",
		expectedSubpath:     "",
	},
	{
		in:                  "git@github.com:devsy-org/devsy-with-branch.git@test-branch",
		expectedRepo:        "git@github.com:devsy-org/devsy-with-branch.git",
		expectedPRReference: "",
		expectedBranch:      branchTestBranch,
		expectedCommit:      "",
		expectedSubpath:     "",
	},
	{
		in:                  "git@github.com:devsy-org/devsy-with-branch.git@test_branch",
		expectedRepo:        "git@github.com:devsy-org/devsy-with-branch.git",
		expectedPRReference: "",
		expectedBranch:      "test_branch",
		expectedCommit:      "",
		expectedSubpath:     "",
	},
	{
		in:                  "ssh://git@github.com:devsy-org/devsy.git@test_branch",
		expectedRepo:        "ssh://git@github.com:devsy-org/devsy.git",
		expectedPRReference: "",
		expectedBranch:      "test_branch",
		expectedCommit:      "",
		expectedSubpath:     "",
	},
	{
		in:                  "github.com/devsy-org/devsy-without-protocol-with-slash.git@user/branch",
		expectedRepo:        repoDevsyNoProtoSlash,
		expectedPRReference: "",
		expectedBranch:      "user/branch",
		expectedCommit:      "",
		expectedSubpath:     "",
	},
	{
		in:                  "git@github.com:devsy-org/devsy-with-slash.git@user/branch",
		expectedRepo:        "git@github.com:devsy-org/devsy-with-slash.git",
		expectedPRReference: "",
		expectedBranch:      "user/branch",
		expectedCommit:      "",
		expectedSubpath:     "",
	},
	{
		in:                  "github.com/devsy-org/devsy.git@sha256:905ffb0",
		expectedRepo:        repoDevsyHTTPS,
		expectedPRReference: "",
		expectedBranch:      "",
		expectedCommit:      testCommitSHA,
		expectedSubpath:     "",
	},
	{
		in:                  "git@github.com:devsy-org/devsy.git@sha256:905ffb0",
		expectedRepo:        "git@github.com:devsy-org/devsy.git",
		expectedPRReference: "",
		expectedBranch:      "",
		expectedCommit:      testCommitSHA,
		expectedSubpath:     "",
	},
	{
		in:                  "github.com/devsy-org/devsy.git@pull/996/head",
		expectedRepo:        repoDevsyHTTPS,
		expectedPRReference: testPRRef,
		expectedBranch:      "",
		expectedCommit:      "",
		expectedSubpath:     "",
	},
	{
		in:                  "git@github.com:devsy-org/devsy.git@pull/996/head",
		expectedRepo:        "git@github.com:devsy-org/devsy.git",
		expectedPRReference: testPRRef,
		expectedBranch:      "",
		expectedCommit:      "",
		expectedSubpath:     "",
	},
	{
		in:                  "git@gitlab.com:h3upperbounds/data/data-team.git@merge-requests/7125/head",
		expectedRepo:        "git@gitlab.com:h3upperbounds/data/data-team.git",
		expectedPRReference: "merge-requests/7125/head",
		expectedBranch:      "",
		expectedCommit:      "",
		expectedSubpath:     "",
	},
	{
		in:                  "github.com/devsy-org/devsy-without-protocol-with-slash.git@subpath:/test/path",
		expectedRepo:        repoDevsyNoProtoSlash,
		expectedPRReference: "",
		expectedBranch:      "",
		expectedCommit:      "",
		expectedSubpath:     testSubpathVal,
	},
	{
		in:                  "github.com/devsy-org/devsy-without-protocol-with-slash.git@subpath:/test/path/",
		expectedRepo:        repoDevsyNoProtoSlash,
		expectedPRReference: "",
		expectedBranch:      "",
		expectedCommit:      "",
		expectedSubpath:     testSubpathVal,
	},
	{
		in:                  "https://my_prefix@github.com/devsy-org/devsy.git@test-branch",
		expectedRepo:        "https://my_prefix@github.com/devsy-org/devsy.git",
		expectedPRReference: "",
		expectedBranch:      branchTestBranch,
		expectedCommit:      "",
		expectedSubpath:     "",
	},
	{
		in:                  "https://test@dev.azure.com/org/project/_git/repo@dev",
		expectedRepo:        "https://test@dev.azure.com/org/project/_git/repo",
		expectedPRReference: "",
		expectedBranch:      testBranch,
		expectedCommit:      "",
		expectedSubpath:     "",
	},
	{
		in:                  "https://test@dev.azure.com/org/project/_git/repo@sha256:905ffb0",
		expectedRepo:        "https://test@dev.azure.com/org/project/_git/repo",
		expectedPRReference: "",
		expectedBranch:      "",
		expectedCommit:      testCommitSHA,
		expectedSubpath:     "",
	},
	{
		in:                  "git@ssh.dev.azure.com:v3/org/project/repo@dev",
		expectedRepo:        "git@ssh.dev.azure.com:v3/org/project/repo",
		expectedPRReference: "",
		expectedBranch:      testBranch,
		expectedCommit:      "",
		expectedSubpath:     "",
	},
	{
		in:                  repoFileProject,
		expectedRepo:        repoFileProject,
		expectedPRReference: "",
		expectedBranch:      "",
		expectedCommit:      "",
		expectedSubpath:     "",
	},
	{
		in:                  "file:///workspace/projects/project@dev",
		expectedRepo:        repoFileProject,
		expectedPRReference: "",
		expectedBranch:      testBranch,
		expectedCommit:      "",
		expectedSubpath:     "",
	},
	{
		in:                  "file:///workspace/projects/project@sha256:905ffb0",
		expectedRepo:        repoFileProject,
		expectedPRReference: "",
		expectedBranch:      "",
		expectedCommit:      testCommitSHA,
		expectedSubpath:     "",
	},
	{
		in:                  "file:///workspace/projects/project@subpath:/test/path",
		expectedRepo:        repoFileProject,
		expectedPRReference: "",
		expectedBranch:      "",
		expectedCommit:      "",
		expectedSubpath:     testSubpathVal,
	},
	{
		// WorkspaceSource.String emits "git:<url>"; round-tripping that
		// through NormalizeRepository must not produce "https://git:https://...".
		in:                  "git:https://github.com/devsy-org/devsy.git",
		expectedRepo:        repoDevsyHTTPS,
		expectedPRReference: "",
		expectedBranch:      "",
		expectedCommit:      "",
		expectedSubpath:     "",
	},
}

func TestNormalizeRepository(t *testing.T) {
	for _, testCase := range normalizeRepositoryCases {
		got := NormalizeRepository(testCase.in)
		assert.Check(t, cmp.Equal(testCase.expectedRepo, got.Repository))
		assert.Check(t, cmp.Equal(testCase.expectedPRReference, got.PR))
		assert.Check(t, cmp.Equal(testCase.expectedBranch, got.Branch))
		assert.Check(t, cmp.Equal(testCase.expectedCommit, got.Commit))
		assert.Check(t, cmp.Equal(testCase.expectedSubpath, got.SubPath))
	}
}

func TestGetBranchNameForPRReference(t *testing.T) {
	testCases := []testCaseGetBranchNameForPR{
		{
			in:             testPRRef,
			expectedBranch: "PR996",
		},
		{
			in:             "merge-requests/7125/head",
			expectedBranch: "MR7125",
		},
		{
			in:             "pull/abc/head",
			expectedBranch: "pull/abc/head",
		},
	}

	for _, testCase := range testCases {
		outBranch := GetBranchNameForPR(testCase.in)
		assert.Check(t, cmp.Equal(testCase.expectedBranch, outBranch))
	}
}
