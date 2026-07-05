package git

const (
	binGit    = "git"
	binGitLFS = "git-lfs"

	subClone  = "clone"
	subConfig = "config"

	flagDepth1   = "--depth=1"
	flagBranch   = "--branch"
	flagConfig   = "--config"
	flagProgress = "--progress"
	flagGlobal   = "--global"
	flagSystem   = "--system"
	flagFile     = "--file"
	flagGet      = "--get"

	lfsInstall = "install"
	pkgUpdate  = "update"

	gitAttributesFile = ".gitattributes"

	// Install strategy names.
	strategyApt     = "apt"
	strategyApk     = "apk"
	strategyRelease = "github-release"

	// GOOS values the release installer recognizes.
	osLinux   = "linux"
	osDarwin  = "darwin"
	osWindows = "windows"
)
