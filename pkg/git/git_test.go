package git

import (
	"testing"

	"gotest.tools/assert"
	"gotest.tools/assert/cmp"
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

func TestNormalizeRepository(t *testing.T) {
	testCases := []testCaseNormalizeRepository{
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
			in:                  "https://github.com/devsy-org/devsy.git",
			expectedRepo:        "https://github.com/devsy-org/devsy.git",
			expectedPRReference: "",
			expectedBranch:      "",
			expectedCommit:      "",
			expectedSubpath:     "",
		},
		{
			in:                  "github.com/devsy-org/devsy.git",
			expectedRepo:        "https://github.com/devsy-org/devsy.git",
			expectedPRReference: "",
			expectedBranch:      "",
			expectedCommit:      "",
			expectedSubpath:     "",
		},
		{
			in:                  "github.com/devsy-org/devsy.git@test-branch",
			expectedRepo:        "https://github.com/devsy-org/devsy.git",
			expectedPRReference: "",
			expectedBranch:      "test-branch",
			expectedCommit:      "",
			expectedSubpath:     "",
		},
		{
			in:                  "git@github.com:devsy-org/devsy-with-branch.git@test-branch",
			expectedRepo:        "git@github.com:devsy-org/devsy-with-branch.git",
			expectedPRReference: "",
			expectedBranch:      "test-branch",
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
			expectedRepo:        "https://github.com/devsy-org/devsy-without-protocol-with-slash.git",
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
			expectedRepo:        "https://github.com/devsy-org/devsy.git",
			expectedPRReference: "",
			expectedBranch:      "",
			expectedCommit:      "905ffb0",
			expectedSubpath:     "",
		},
		{
			in:                  "git@github.com:devsy-org/devsy.git@sha256:905ffb0",
			expectedRepo:        "git@github.com:devsy-org/devsy.git",
			expectedPRReference: "",
			expectedBranch:      "",
			expectedCommit:      "905ffb0",
			expectedSubpath:     "",
		},
		{
			in:                  "github.com/devsy-org/devsy.git@pull/996/head",
			expectedRepo:        "https://github.com/devsy-org/devsy.git",
			expectedPRReference: "pull/996/head",
			expectedBranch:      "",
			expectedCommit:      "",
			expectedSubpath:     "",
		},
		{
			in:                  "git@github.com:devsy-org/devsy.git@pull/996/head",
			expectedRepo:        "git@github.com:devsy-org/devsy.git",
			expectedPRReference: "pull/996/head",
			expectedBranch:      "",
			expectedCommit:      "",
			expectedSubpath:     "",
		},
		{
			in:                  "github.com/devsy-org/devsy-without-protocol-with-slash.git@subpath:/test/path",
			expectedRepo:        "https://github.com/devsy-org/devsy-without-protocol-with-slash.git",
			expectedPRReference: "",
			expectedBranch:      "",
			expectedCommit:      "",
			expectedSubpath:     "/test/path",
		},
		{
			in:                  "github.com/devsy-org/devsy-without-protocol-with-slash.git@subpath:/test/path/",
			expectedRepo:        "https://github.com/devsy-org/devsy-without-protocol-with-slash.git",
			expectedPRReference: "",
			expectedBranch:      "",
			expectedCommit:      "",
			expectedSubpath:     "/test/path",
		},
		{
			in:                  "https://my_prefix@github.com/devsy-org/devsy.git@test-branch",
			expectedRepo:        "https://my_prefix@github.com/devsy-org/devsy.git",
			expectedPRReference: "",
			expectedBranch:      "test-branch",
			expectedCommit:      "",
			expectedSubpath:     "",
		},
		{
			in:                  "https://test@dev.azure.com/org/project/_git/repo@dev",
			expectedRepo:        "https://test@dev.azure.com/org/project/_git/repo",
			expectedPRReference: "",
			expectedBranch:      "dev",
			expectedCommit:      "",
			expectedSubpath:     "",
		},
		{
			in:                  "https://test@dev.azure.com/org/project/_git/repo@sha256:905ffb0",
			expectedRepo:        "https://test@dev.azure.com/org/project/_git/repo",
			expectedPRReference: "",
			expectedBranch:      "",
			expectedCommit:      "905ffb0",
			expectedSubpath:     "",
		},
		{
			in:                  "git@ssh.dev.azure.com:v3/org/project/repo@dev",
			expectedRepo:        "git@ssh.dev.azure.com:v3/org/project/repo",
			expectedPRReference: "",
			expectedBranch:      "dev",
			expectedCommit:      "",
			expectedSubpath:     "",
		},
		{
			in:                  "file:///workspace/projects/project",
			expectedRepo:        "file:///workspace/projects/project",
			expectedPRReference: "",
			expectedBranch:      "",
			expectedCommit:      "",
			expectedSubpath:     "",
		},
		{
			in:                  "file:///workspace/projects/project@dev",
			expectedRepo:        "file:///workspace/projects/project",
			expectedPRReference: "",
			expectedBranch:      "dev",
			expectedCommit:      "",
			expectedSubpath:     "",
		},
		{
			in:                  "file:///workspace/projects/project@sha256:905ffb0",
			expectedRepo:        "file:///workspace/projects/project",
			expectedPRReference: "",
			expectedBranch:      "",
			expectedCommit:      "905ffb0",
			expectedSubpath:     "",
		},
		{
			in:                  "file:///workspace/projects/project@subpath:/test/path",
			expectedRepo:        "file:///workspace/projects/project",
			expectedPRReference: "",
			expectedBranch:      "",
			expectedCommit:      "",
			expectedSubpath:     "/test/path",
		},
		{
			// WorkspaceSource.String emits "git:<url>"; round-tripping that
			// through NormalizeRepository must not produce "https://git:https://...".
			in:                  "git:https://github.com/devsy-org/devsy.git",
			expectedRepo:        "https://github.com/devsy-org/devsy.git",
			expectedPRReference: "",
			expectedBranch:      "",
			expectedCommit:      "",
			expectedSubpath:     "",
		},
	}

	for _, testCase := range testCases {
		outRepo, outPRReference, outBranch, outCommit, outSubpath := NormalizeRepository(
			testCase.in,
		)
		assert.Check(t, cmp.Equal(testCase.expectedRepo, outRepo))
		assert.Check(t, cmp.Equal(testCase.expectedPRReference, outPRReference))
		assert.Check(t, cmp.Equal(testCase.expectedBranch, outBranch))
		assert.Check(t, cmp.Equal(testCase.expectedCommit, outCommit))
		assert.Check(t, cmp.Equal(testCase.expectedSubpath, outSubpath))
	}
}

func TestGetBranchNameForPRReference(t *testing.T) {
	testCases := []testCaseGetBranchNameForPR{
		{
			in:             "pull/996/head",
			expectedBranch: "PR996",
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
