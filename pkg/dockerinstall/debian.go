package dockerinstall

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// aptFlags configures apt-get to fail fast on transient network issues
// instead of hanging indefinitely, retrying a bounded number of times first.
var aptFlags = fmt.Sprintf(
	`-o Acquire::http::Timeout="%d" -o Acquire::https::Timeout="%d" -o Acquire::Retries="%d"`,
	AptTimeoutSeconds, AptTimeoutSeconds, AptRetries,
)

type DebianInstaller struct {
	distro   *Distro
	opts     *InstallOptions
	executor *Executor
}

func NewDebianInstaller(distro *Distro, opts *InstallOptions) *DebianInstaller {
	return &DebianInstaller{
		distro:   distro,
		opts:     opts,
		executor: NewExecutor(opts),
	}
}

func (i *DebianInstaller) Install(ctx context.Context, shC string) error {
	if err := i.setupRepo(ctx, shC); err != nil {
		return err
	}

	pkgVersion, cliPkgVersion, err := i.findVersions()
	if err != nil {
		return err
	}
	pkgs := BuildPackageList(i.opts.version, pkgVersion, cliPkgVersion)

	installCmd := fmt.Sprintf(
		"DEBIAN_FRONTEND=noninteractive apt-get %s install -y -qq --no-install-recommends %s >/dev/null",
		aptFlags,
		pkgs,
	)
	if err := i.executor.RunWithRetry(ctx, shC, installCmd, DefaultTimeout); err != nil {
		return err
	}

	return nil
}

func (i *DebianInstaller) setupRepo(ctx context.Context, shC string) error {
	preReqs := "apt-transport-https ca-certificates curl"
	if !commandExists("gpg") {
		preReqs += " gnupg"
	}

	if !i.distro.HasCodename() {
		return fmt.Errorf(
			"invalid or missing codename for %s: %s (VERSION_CODENAME not found in /etc/os-release)",
			i.distro.ID,
			i.distro.Version,
		)
	}

	aptRepo := fmt.Sprintf(
		"deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] %s/linux/%s %s %s",
		i.opts.downloadURL,
		i.distro.ID,
		i.distro.Version,
		i.opts.channel,
	)

	cmdAptUpdate := fmt.Sprintf("apt-get %s update -qq >/dev/null", aptFlags)

	cmdAptInstall := fmt.Sprintf(
		"DEBIAN_FRONTEND=noninteractive apt-get %s install -y -qq %s >/dev/null",
		aptFlags, preReqs,
	)

	cmdKeyringDir := "mkdir -p /etc/apt/keyrings && chmod -R 0755 /etc/apt/keyrings"

	cmdDockerGPG := fmt.Sprintf(
		"curl -fsSL \"%s/linux/%s/gpg\" | gpg --dearmor --yes -o /etc/apt/keyrings/docker.gpg",
		i.opts.downloadURL, i.distro.ID,
	)

	cmds := []string{
		cmdAptUpdate,
		cmdAptInstall,
		cmdKeyringDir,
		cmdDockerGPG,
		"chmod a+r /etc/apt/keyrings/docker.gpg",
		fmt.Sprintf("echo %q > /etc/apt/sources.list.d/docker.list", aptRepo),
		cmdAptUpdate,
	}

	return i.executor.RunCommandsWithRetry(ctx, shC, cmds, DefaultTimeout)
}

func (i *DebianInstaller) findVersions() (string, string, error) {
	if i.opts.version == "" || i.opts.dryRun {
		if i.opts.dryRun {
			fprintln(i.opts.stdout, "# WARNING: VERSION pinning is not supported in DRY_RUN")
		}
		return "", "", nil
	}

	pkgVersion, err := i.findPackageVersion("docker-ce")
	if err != nil {
		return "", "", err
	}

	cliPkgVersion := ""
	if versionGte(i.opts.version, ubuntuRelease1809) {
		cliPkgVersion, err = i.findPackageVersion("docker-ce-cli")
		if err != nil {
			return "", "", err
		}
	}

	return pkgVersion, cliPkgVersion, nil
}

func (i *DebianInstaller) findPackageVersion(pkgName string) (string, error) {
	pkgPattern := strings.ReplaceAll(i.opts.version, "-ce-", "~ce~.*")
	pkgPattern = strings.ReplaceAll(pkgPattern, "-", ".*")
	pkgPattern = strings.ReplaceAll(pkgPattern, ".", "\\.")
	searchCmd := fmt.Sprintf(
		"apt-cache madison %q | grep %q | head -1 | awk '{$1=$1};1' | cut -d' ' -f 3",
		pkgName, pkgPattern,
	)

	fprintln(i.opts.stdout, "INFO: Searching repository for VERSION '"+i.opts.version+"'")
	fprintln(i.opts.stdout, "INFO: "+searchCmd)

	//nolint:gosec // Intentional shell command for apt repository search
	cmd := exec.Command("sh", "-c", searchCmd)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("apt-cache madison %s failed: %w", pkgName, err)
	}
	if len(out) == 0 {
		return "", fmt.Errorf(
			"version %q not found in apt-cache madison results for %s",
			i.opts.version,
			pkgName,
		)
	}
	return "=" + strings.TrimSpace(string(out)), nil
}
