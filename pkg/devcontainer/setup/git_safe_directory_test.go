package setup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/suite"
)

type GitSafeDirectoryTestSuite struct {
	suite.Suite
}

func TestGitSafeDirectoryTestSuite(t *testing.T) {
	suite.Run(t, new(GitSafeDirectoryTestSuite))
}

func (s *GitSafeDirectoryTestSuite) TestCandidateNilResult() {
	s.Empty(gitWorkspaceCandidate(nil))
}

func (s *GitSafeDirectoryTestSuite) TestCandidateNilSubstitutionContext() {
	s.Empty(gitWorkspaceCandidate(&config.Result{}))
}

func (s *GitSafeDirectoryTestSuite) TestCandidateWorkspaceMountGitRoot() {
	repoDir := s.T().TempDir()
	err := os.Mkdir(filepath.Join(repoDir, ".git"), 0o750)
	s.Require().NoError(err)

	result := &config.Result{
		SubstitutionContext: &config.SubstitutionContext{
			WorkspaceMount:           fmt.Sprintf("type=bind,source=/host/repo,target=%s", repoDir),
			ContainerWorkspaceFolder: repoDir,
		},
	}
	s.Equal(filepath.Clean(repoDir), gitWorkspaceCandidate(result))
}

func (s *GitSafeDirectoryTestSuite) TestCandidateWorkspaceFolderBelowMountRoot() {
	repoDir := s.T().TempDir()
	err := os.Mkdir(filepath.Join(repoDir, ".git"), 0o750)
	s.Require().NoError(err)
	subDir := filepath.Join(repoDir, "src")
	err = os.Mkdir(subDir, 0o750)
	s.Require().NoError(err)

	result := &config.Result{
		SubstitutionContext: &config.SubstitutionContext{
			WorkspaceMount:           fmt.Sprintf("type=bind,source=/host/repo,target=%s", repoDir),
			ContainerWorkspaceFolder: subDir,
		},
	}
	s.Equal(filepath.Clean(repoDir), gitWorkspaceCandidate(result))
}

func (s *GitSafeDirectoryTestSuite) TestCandidateComposeNoMount() {
	repoDir := s.T().TempDir()
	err := os.Mkdir(filepath.Join(repoDir, ".git"), 0o750)
	s.Require().NoError(err)

	result := &config.Result{
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: repoDir,
		},
	}
	s.Equal(filepath.Clean(repoDir), gitWorkspaceCandidate(result))
}

func (s *GitSafeDirectoryTestSuite) TestCandidateRegularFileGit() {
	repoDir := s.T().TempDir()
	gitFile := filepath.Join(repoDir, ".git")
	err := os.WriteFile(gitFile, []byte("gitdir: /some/path/.git/worktrees/test\n"), 0o600)
	s.Require().NoError(err)

	result := &config.Result{
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: repoDir,
		},
	}
	s.Equal(filepath.Clean(repoDir), gitWorkspaceCandidate(result))
}

func (s *GitSafeDirectoryTestSuite) TestCandidateSymlinkGitRejected() {
	repoDir := s.T().TempDir()
	realGitDir := filepath.Join(s.T().TempDir(), "real.git")
	err := os.Mkdir(realGitDir, 0o750)
	s.Require().NoError(err)
	err = os.Symlink(realGitDir, filepath.Join(repoDir, ".git"))
	s.Require().NoError(err)

	result := &config.Result{
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: repoDir,
		},
	}
	s.Empty(gitWorkspaceCandidate(result))
}

func (s *GitSafeDirectoryTestSuite) TestCandidateSymlinkWorkspaceRejected() {
	realDir := s.T().TempDir()
	err := os.Mkdir(filepath.Join(realDir, ".git"), 0o750)
	s.Require().NoError(err)
	linkDir := filepath.Join(s.T().TempDir(), "link-workspace")
	err = os.Symlink(realDir, linkDir)
	s.Require().NoError(err)

	result := &config.Result{
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: linkDir,
		},
	}
	s.Empty(gitWorkspaceCandidate(result))
}

func (s *GitSafeDirectoryTestSuite) TestCandidateNonGitWorkspaceRejected() {
	result := &config.Result{
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: s.T().TempDir(),
		},
	}
	s.Empty(gitWorkspaceCandidate(result))
}

func (s *GitSafeDirectoryTestSuite) TestCandidateRootPathRejected() {
	result := &config.Result{
		SubstitutionContext: &config.SubstitutionContext{
			WorkspaceMount:           "type=bind,source=/host,target=/",
			ContainerWorkspaceFolder: "/",
		},
	}
	s.Empty(gitWorkspaceCandidate(result))
}

func (s *GitSafeDirectoryTestSuite) TestCandidateWorkspacesParentRejected() {
	result := &config.Result{
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: "/workspaces",
		},
	}
	s.Empty(gitWorkspaceCandidate(result))
}

func (s *GitSafeDirectoryTestSuite) TestCandidateRelativePathRejected() {
	result := &config.Result{
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: "relative/path",
		},
	}
	s.Empty(gitWorkspaceCandidate(result))
}

func (s *GitSafeDirectoryTestSuite) TestCandidateWildcardPathRejected() {
	result := &config.Result{
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: "/workspaces/*",
		},
	}
	s.Empty(gitWorkspaceCandidate(result))
}

func (s *GitSafeDirectoryTestSuite) TestSetupAddsExactSafeDirectory() {
	if _, err := exec.LookPath("git"); err != nil {
		s.T().Skip("git not found; skipping safe.directory system config tests")
	}

	ctx := context.Background()
	result, _, repoDir := s.createIsolatedGitSetup("vscode")

	err := setupGitSafeDirectory(ctx, result)
	s.Require().NoError(err)

	entries, err := getGitSafeDirectories(ctx)
	s.Require().NoError(err)
	s.Equal([]string{repoDir}, entries)
}

func (s *GitSafeDirectoryTestSuite) TestSetupIdempotent() {
	if _, err := exec.LookPath("git"); err != nil {
		s.T().Skip("git not found; skipping safe.directory system config tests")
	}

	ctx := context.Background()
	result, _, repoDir := s.createIsolatedGitSetup("vscode")

	s.Require().NoError(setupGitSafeDirectory(ctx, result))
	s.Require().NoError(setupGitSafeDirectory(ctx, result))

	entries, err := getGitSafeDirectories(ctx)
	s.Require().NoError(err)
	s.Equal([]string{repoDir}, entries)
}

func (s *GitSafeDirectoryTestSuite) TestSetupPreservesUnrelatedEntries() {
	if _, err := exec.LookPath("git"); err != nil {
		s.T().Skip("git not found; skipping safe.directory system config tests")
	}

	ctx := context.Background()
	result, configFile, repoDir := s.createIsolatedGitSetup("vscode")

	preseedCmd := exec.Command(
		"git",
		"config",
		"--system",
		"--add",
		"safe.directory",
		"/some/other/repo",
	)
	preseedCmd.Env = append(os.Environ(), "GIT_CONFIG_SYSTEM="+configFile)
	out, err := preseedCmd.CombinedOutput()
	s.Require().NoError(err, string(out))

	s.Require().NoError(setupGitSafeDirectory(ctx, result))

	entries, err := getGitSafeDirectories(ctx)
	s.Require().NoError(err)
	s.Equal([]string{"/some/other/repo", repoDir}, entries)
}

func (s *GitSafeDirectoryTestSuite) TestSetupDoesNotCreateWildcard() {
	if _, err := exec.LookPath("git"); err != nil {
		s.T().Skip("git not found; skipping safe.directory system config tests")
	}

	ctx := context.Background()
	result, _, _ := s.createIsolatedGitSetup("vscode")

	s.Require().NoError(setupGitSafeDirectory(ctx, result))

	entries, err := getGitSafeDirectories(ctx)
	s.Require().NoError(err)
	s.NotContains(entries, "*")
	s.NotContains(entries, "/workspaces/*")
}

func (s *GitSafeDirectoryTestSuite) TestSetupExistingWildcardNotModified() {
	if _, err := exec.LookPath("git"); err != nil {
		s.T().Skip("git not found; skipping safe.directory system config tests")
	}

	ctx := context.Background()
	result, configFile, _ := s.createIsolatedGitSetup("vscode")

	preseedCmd := exec.Command("git", "config", "--system", "--add", "safe.directory", "*")
	preseedCmd.Env = append(os.Environ(), "GIT_CONFIG_SYSTEM="+configFile)
	out, err := preseedCmd.CombinedOutput()
	s.Require().NoError(err, string(out))

	s.Require().NoError(setupGitSafeDirectory(ctx, result))

	entries, err := getGitSafeDirectories(ctx)
	s.Require().NoError(err)
	s.Equal([]string{"*"}, entries)
}

func (s *GitSafeDirectoryTestSuite) TestSetupRootRemoteUserSkips() {
	ctx := context.Background()
	result, _, _ := s.createIsolatedGitSetup("root")

	s.Require().NoError(setupGitSafeDirectory(ctx, result))

	entries, err := getGitSafeDirectories(ctx)
	s.Require().NoError(err)
	s.Empty(entries)
}

func (s *GitSafeDirectoryTestSuite) TestSetupGitAbsentSkips() {
	origLookPath := gitLookPath
	gitLookPath = func(file string) (string, error) {
		return "", exec.ErrNotFound
	}
	defer func() { gitLookPath = origLookPath }()

	ctx := context.Background()
	result, _, _ := s.createIsolatedGitSetup("vscode")

	s.Require().NoError(setupGitSafeDirectory(ctx, result))

	gitLookPath = origLookPath
	entries, err := getGitSafeDirectories(ctx)
	s.Require().NoError(err)
	s.Empty(entries)
}

func (s *GitSafeDirectoryTestSuite) TestSetupWriteFailureReturnsError() {
	ctx := context.Background()
	result, _, _ := s.createIsolatedGitSetup("vscode")

	regularFile := filepath.Join(s.T().TempDir(), "not-a-dir")
	err := os.WriteFile(regularFile, []byte("file"), 0o600)
	s.Require().NoError(err)
	s.T().Setenv("GIT_CONFIG_SYSTEM", filepath.Join(regularFile, "gitconfig"))

	err = setupGitSafeDirectory(ctx, result)
	s.Error(err)
}

func (s *GitSafeDirectoryTestSuite) createIsolatedGitSetup(
	user string,
) (*config.Result, string, string) {
	tempDir := s.T().TempDir()
	configFile := filepath.Join(tempDir, "gitconfig")
	s.T().Setenv("GIT_CONFIG_SYSTEM", configFile)

	repoDir := s.T().TempDir()
	err := os.Mkdir(filepath.Join(repoDir, ".git"), 0o750)
	s.Require().NoError(err)

	result := &config.Result{
		MergedConfig: &config.MergedDevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{
				RemoteUser: user,
			},
		},
		SubstitutionContext: &config.SubstitutionContext{
			WorkspaceMount:           fmt.Sprintf("type=bind,source=/host/repo,target=%s", repoDir),
			ContainerWorkspaceFolder: repoDir,
		},
	}

	return result, configFile, filepath.Clean(repoDir)
}
