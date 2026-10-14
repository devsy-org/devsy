package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

const (
	stageValidate = "validate-environment"
	stageWSLCheck = "wsl-preflight"
	stageInstall  = "install-packages"
	stageDaemon   = "start-dockerd"
	stageCLIName  = "install-docker-cli"
	stageKey      = "ssh-identity"
	stageSSHConf  = "configure-sshd"
	stageSSHStart = "start-sshd"
	stageEndpoint = "ssh-endpoint"
	stageOracles  = "verify-oracles"

	daemonReadyAttempts = 60
	sshdReadyAttempts   = 30
	sshAuthAttempts     = 5
	pollGap             = time.Second
	sshAuthGap          = 2 * time.Second

	sshConnectTimeout = 10 * time.Second
	identityFileName  = "id_ed25519"
	knownHostsName    = "known_hosts"
	sshConfigBackupNm = "ssh-config-original"
	sshConfigACLNm    = "ssh-config.acl"

	configFilePerm = stateFilePerm
)

// validateEnvironment is W1: tools, distribution and the pre-existing state of
// the target host.
func (d *dockerWSL) validateEnvironment(ctx context.Context) error {
	for _, name := range []string{toolWSL, toolSSH, toolSSHKeygen, toolICACLS} {
		if _, err := d.tool(name); err != nil {
			return err
		}
	}
	if _, ok := lookupEnv(d.env, "USERNAME", true); !ok || d.guard.userConfig == "" {
		return configError("USERNAME and USERPROFILE must be set to protect the SSH identity")
	}
	path, _ := d.tool(toolWSL)
	res, err := d.run(ctx, stageValidate, CodeDockerWSLDistroGone, Command{
		Path:    path,
		Args:    []string{"-l", "-q"},
		Env:     []string{"WSL_UTF8=1"},
		Timeout: wslQuickTimeout,
	})
	if err != nil {
		return d.asCode(err, CodeDockerWSLDistroGone)
	}
	names := parseWSLDistributions(res.Stdout)
	if !containsFold(names, d.state.Distribution) {
		return newStageError(CodeDockerWSLDistroGone, stageValidate,
			fmt.Errorf("distribution %q is not installed", d.state.Distribution)).
			WithDetail("installed: " + strings.Join(names, ", "))
	}
	return d.record(func(s *dockerWSLState) { s.Stage = stageDistro })
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

// asCode re-labels a StageError's code, keeping its stage and detail.
func (d *dockerWSL) asCode(err error, code Code) error {
	if se, ok := errors.AsType[*StageError](err); ok && se.Code != code {
		cp := *se
		cp.Code = code
		return &cp
	}
	return err
}

// parseKeyValues reads KEY=value lines.
func parseKeyValues(out string) map[string]string {
	kv := map[string]string{}
	for line := range strings.Lines(out) {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			kv[k] = v
		}
	}
	return kv
}

// preflightWSL records what exists before installation and refuses to touch a
// daemon or account this run did not create.
func (d *dockerWSL) preflightWSL(ctx context.Context) error {
	res, err := d.script(ctx, stageWSLCheck, CodeDockerWSLInstall,
		wslScript{body: preflightScript, timeout: wslQuickTimeout})
	if err != nil {
		return err
	}
	kv := parseKeyValues(res.Stdout)
	_, _ = fmt.Fprintf(d.deps.Out,
		"wsl preflight: socket=%s docker_installed=%s systemd=%s ci_user=%s leftovers=%s\n",
		kv["socket"], kv["docker_installed"], kv["systemd"], kv["ci_user"], kv["leftovers"])
	switch {
	case kv["socket"] != "absent":
		return newStageError(CodeDockerWSLDaemon, stageWSLCheck, fmt.Errorf(
			"%s already exists: a Docker daemon this run did not start is using it; refusing to stop it",
			dockerSocketPath,
		))
	case kv["ci_user"] != "absent":
		return configError("Linux user %s already exists and is not owned by this run", ciUser)
	case kv["leftovers"] != "no":
		return configError(
			"a previous run left /run/devsy-ci, /etc/ssh/devsy-ci or /tmp/devsy-ci behind",
		)
	}
	return nil
}

// installPackages is W2: Docker Engine and sshd from signed repositories, then
// stop whatever service the installer started.
func (d *dockerWSL) installPackages(ctx context.Context) error {
	res, err := d.script(ctx, stageInstall, CodeDockerWSLInstall,
		wslScript{body: installPackagesScript, timeout: wslInstallTimeout})
	if err != nil {
		return err
	}
	var packages []string
	for line := range strings.Lines(res.Stdout) {
		if pkg, ok := strings.CutPrefix(strings.TrimSpace(line), "package "); ok {
			packages = append(packages, pkg)
		}
	}
	_, _ = fmt.Fprintf(d.deps.Out, "installed packages: %s\n", strings.Join(packages, " "))
	if err := d.record(func(s *dockerWSLState) {
		s.Packages, s.Stage = packages, stagePackages
	}); err != nil {
		return err
	}
	_, err = d.script(ctx, stageInstall, CodeDockerWSLDaemon,
		wslScript{body: stopInstallerDaemonScript, timeout: wslScriptTimeout})
	return err
}

// startDockerd is W3: a detached dockerd with its own pidfile and log, and a
// recorded daemon ID.
func (d *dockerWSL) startDockerd(ctx context.Context) error {
	if err := d.record(
		func(s *dockerWSLState) { s.Owned.DockerPidfile = dockerPidfile },
	); err != nil {
		return err
	}
	if _, err := d.script(ctx, stageDaemon, CodeDockerWSLDaemon,
		wslScript{body: prepareRuntimeScript, timeout: wslQuickTimeout}); err != nil {
		return err
	}
	if err := d.startDetached(stageDaemon, CodeDockerWSLDaemon, dockerdLaunchScript); err != nil {
		return err
	}
	id, err := d.waitForDaemon(ctx)
	if err != nil {
		return err
	}
	if err := d.requireNoDockerTCP(ctx, stageDaemon); err != nil {
		return err
	}
	return d.record(func(s *dockerWSLState) { s.DaemonID, s.Stage = id, stageDockerd })
}

// waitForDaemon polls docker info inside WSL for a bounded number of attempts.
func (d *dockerWSL) waitForDaemon(ctx context.Context) (string, error) {
	var last error
	for i := range daemonReadyAttempts {
		id, err := d.wslDaemonID(ctx, stageDaemon)
		if err == nil {
			return id, nil
		}
		last = err
		if !d.pause(ctx, i, daemonReadyAttempts, pollGap) {
			break
		}
	}
	return "", d.withLogTail(ctx, newStageError(CodeDockerWSLDaemon, stageDaemon,
		fmt.Errorf("dockerd did not become ready on %s: %w", dockerSocketPath, last)))
}

// withLogTail attaches a fresh, bounded WSL diagnostics tail to err.
func (d *dockerWSL) withLogTail(ctx context.Context, se *StageError) error {
	diagCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), wslQuickTimeout)
	defer cancel()
	res, _ := d.script(diagCtx, "diagnostics", se.Code,
		wslScript{body: diagnosticsScript, timeout: wslQuickTimeout})
	return se.WithDetail(res.Stdout + res.Stderr)
}

// installDockerCLI is W4: the pinned, checksum-verified Windows CLI and an
// isolated DOCKER_CONFIG. Exports happen only after every proof.
func (d *dockerWSL) installDockerCLI(ctx context.Context) error {
	fx := d.guard.fixture()
	cliDir := filepath.Join(fx, "docker-cli")
	cfgDir := filepath.Join(fx, "docker-config")
	if err := d.record(func(s *dockerWSLState) {
		s.Owned.DockerCLIDirectory, s.Owned.DockerConfigDir = cliDir, cfgDir
	}); err != nil {
		return err
	}
	binDir, err := fetchDockerCLI(ctx, d.deps, d.cli, cliDir)
	if err != nil {
		return err
	}
	if err := d.deps.Files.MkdirAll(cfgDir, stateDirPerm); err != nil {
		return newStageError(CodeDockerWSLInstall, stageCLIName, err)
	}
	if err := d.record(
		func(s *dockerWSLState) { s.Owned.DockerBinDirectory = binDir },
	); err != nil {
		return err
	}
	res, err := d.run(ctx, stageCLIName, CodeDockerWSLInstall,
		d.dockerCommand("", "--version"))
	if err != nil {
		return err
	}
	if !strings.Contains(res.Stdout, "Docker version") {
		return newStageError(
			CodeDockerWSLInstall,
			stageCLIName,
			errors.New(
				"docker.exe --version did not report a Docker version",
			),
		).WithDetail(res.Stdout)
	}
	return d.record(func(s *dockerWSLState) { s.Stage = stageCLI })
}

// createIdentity is the Windows half of W5: an ephemeral Ed25519 key whose ACL
// is restricted and verified.
func (d *dockerWSL) createIdentity(ctx context.Context) error {
	fx := d.guard.fixture()
	key := filepath.Join(fx, identityFileName)
	if err := d.deps.Files.MkdirAll(fx, stateDirPerm); err != nil {
		return newStageError(CodeDockerWSLSSHNotReady, stageKey, err)
	}
	if err := d.record(func(s *dockerWSLState) { s.Owned.SSHIdentityFile = key }); err != nil {
		return err
	}
	keygen, err := d.tool(toolSSHKeygen)
	if err != nil {
		return err
	}
	if _, err := d.run(ctx, stageKey, CodeDockerWSLSSHNotReady, Command{
		Path: keygen,
		Args: []string{
			"-q",
			"-t",
			"ed25519",
			"-N",
			"",
			"-C",
			"devsy-ci-" + d.state.RunID,
			"-f",
			key,
		},
		Timeout: wslQuickTimeout,
	}); err != nil {
		return err
	}
	if err := d.restrictKeyACL(ctx, key); err != nil {
		return err
	}
	return d.record(func(s *dockerWSLState) { s.Stage = stageIdentity })
}

func (d *dockerWSL) restrictKeyACL(ctx context.Context, key string) error {
	icacls, err := d.tool(toolICACLS)
	if err != nil {
		return err
	}
	user, _ := lookupEnv(d.env, "USERNAME", true)
	if _, err := d.run(ctx, stageKey, CodeDockerWSLSSHNotReady, Command{
		Path: icacls, Args: icaclsRestrictArgs(key, user), Timeout: wslQuickTimeout,
	}); err != nil {
		return err
	}
	res, err := d.run(ctx, stageKey, CodeDockerWSLSSHNotReady, Command{
		Path: icacls, Args: []string{key}, Timeout: wslQuickTimeout,
	})
	if err != nil {
		return err
	}
	if err := verifyKeyACL(res.Stdout, key, user); err != nil {
		return newStageError(CodeDockerWSLSSHNotReady, stageKey, err)
	}
	return nil
}

func userMarker(runID string) string { return "devsy-ci:" + runID }

// configureSSHServer is the Linux half of W5: the account, the dedicated sshd
// configuration and the authorized public key. Each resource is registered
// before the call that may partially create it.
func (d *dockerWSL) configureSSHServer(ctx context.Context) error {
	if err := d.record(func(s *dockerWSLState) { s.Owned.CIUser = ciUser }); err != nil {
		return err
	}
	if _, err := d.script(ctx, stageSSHConf, CodeDockerWSLSSHNotReady, wslScript{
		body: createUserScript, args: []string{ciUser, userMarker(d.state.RunID)},
		timeout: wslScriptTimeout,
	}); err != nil {
		return err
	}
	if err := d.record(
		func(s *dockerWSLState) { s.Owned.SSHDConfigDir = sshdConfigDir },
	); err != nil {
		return err
	}
	if _, err := d.script(ctx, stageSSHConf, CodeDockerWSLSSHNotReady,
		wslScript{body: configureSSHDScript, timeout: wslScriptTimeout}); err != nil {
		return err
	}
	if err := d.installAuthorizedKey(ctx); err != nil {
		return err
	}
	return d.record(func(s *dockerWSLState) { s.Stage = stageSSHServerConf })
}

func (d *dockerWSL) installAuthorizedKey(ctx context.Context) error {
	pub, err := d.deps.Files.ReadFile(d.state.Owned.SSHIdentityFile + ".pub")
	if err != nil {
		return newStageError(CodeDockerWSLSSHNotReady, stageSSHConf, err)
	}
	key, err := parseIdentityPublicKey(pub)
	if err != nil {
		return newStageError(CodeDockerWSLSSHNotReady, stageSSHConf, err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(key))
	_, err = d.script(ctx, stageSSHConf, CodeDockerWSLSSHNotReady, wslScript{
		body: installKeyScript, args: []string{ciUser, encoded}, timeout: wslScriptTimeout,
	})
	return err
}

// startSSHD is the first half of W6: a detached, owned sshd, its listener, the
// trusted host key and the VM addresses.
func (d *dockerWSL) startSSHD(ctx context.Context) error {
	if err := d.record(func(s *dockerWSLState) { s.Owned.SSHDPidfile = sshdPidfile }); err != nil {
		return err
	}
	if err := d.startDetached(
		stageSSHStart,
		CodeDockerWSLSSHNotReady,
		sshdLaunchScript,
	); err != nil {
		return err
	}
	if err := d.waitForListener(ctx); err != nil {
		return err
	}
	res, err := d.wsl(ctx, stageSSHStart, CodeDockerWSLSSHNotReady, wslCall{
		user: rootUser, argv: []string{"cat", sshdHostKeyPub}, timeout: wslQuickTimeout,
	})
	if err != nil {
		return err
	}
	if d.hostKey, err = parseHostKey(res.Stdout); err != nil {
		return newStageError(CodeDockerWSLSSHNotReady, stageSSHStart, err)
	}
	ips, err := d.wsl(ctx, stageSSHStart, CodeDockerWSLSSHNotReady, wslCall{
		user: rootUser, argv: []string{"hostname", "-I"}, timeout: wslQuickTimeout,
	})
	if err != nil {
		return err
	}
	d.vmIPs = parseVMAddresses(ips.Stdout)
	return d.record(func(s *dockerWSLState) { s.Stage = stageSSHD })
}

func (d *dockerWSL) waitForListener(ctx context.Context) error {
	var last error
	for i := range sshdReadyAttempts {
		listening, err := d.listenerPresent(ctx, stageSSHStart, sshPortHex)
		if listening {
			return nil
		}
		last = err
		if !d.pause(ctx, i, sshdReadyAttempts, pollGap) {
			break
		}
	}
	if last == nil {
		last = fmt.Errorf("nothing listens on port %s", sshPort)
	}
	return d.withLogTail(ctx, newStageError(CodeDockerWSLSSHNotReady, stageSSHStart,
		fmt.Errorf("sshd did not start listening: %w", last)))
}

// hostSpec derives the ssh_config block for the chosen address.
func (d *dockerWSL) hostSpec(addr string) sshHostSpec {
	spec := sshHostSpec{
		RunID:        d.state.RunID,
		Alias:        "devsy-ci-" + d.state.RunID,
		HostName:     addr,
		Port:         sshPort,
		IdentityFile: d.state.Owned.SSHIdentityFile,
		KnownHosts:   filepath.Join(d.guard.fixture(), knownHostsName),
		HostKeyAlias: d.opts.EndpointMode == sshModeAlias,
	}
	if !spec.HostKeyAlias {
		spec.Alias = addr
	}
	return spec
}

func (spec sshHostSpec) endpoint() string {
	if spec.HostKeyAlias {
		return "ssh://" + ciUser + "@" + spec.Alias
	}
	return "ssh://" + ciUser + "@" + spec.HostName + ":" + spec.Port
}

// configureSSHClient is the second half of W6: pick a reachable address, pin
// the host key in an isolated known_hosts, install the scoped ssh_config block
// and authenticate with exactly what the Docker CLI will use.
func (d *dockerWSL) configureSSHClient(ctx context.Context) error {
	addr, err := selectSSHAddress(ctx, d.deps, sshCandidates(d.vmIPs))
	if err != nil {
		return d.withLogTail(ctx, newStageError(CodeDockerWSLSSHNotReady, stageEndpoint, err))
	}
	spec := d.hostSpec(addr)
	if err := d.record(
		func(s *dockerWSLState) { s.Owned.KnownHostsFile = spec.KnownHosts },
	); err != nil {
		return err
	}
	content := knownHostsContent(spec, d.hostKey)
	if err := d.deps.Files.WriteFile(spec.KnownHosts, []byte(content), configFilePerm); err != nil {
		return newStageError(CodeDockerWSLSSHNotReady, stageEndpoint, err)
	}
	if err := d.installSSHConfig(ctx, spec); err != nil {
		return err
	}
	if err := d.record(func(s *dockerWSLState) {
		s.SSHHostAlias, s.SSHEndpoint = spec.Alias, spec.endpoint()
	}); err != nil {
		return err
	}
	if err := d.verifySSHAuth(ctx, spec.Alias); err != nil {
		return err
	}
	return d.record(func(s *dockerWSLState) { s.Stage = stageSSHConfigured })
}

// installSSHConfig backs up the user's ssh_config byte for byte (and its ACLs),
// registers the backup, then prepends the scoped block atomically.
func (d *dockerWSL) installSSHConfig(ctx context.Context, spec sshHostSpec) error {
	cfg := d.guard.userConfig
	original, err := d.deps.Files.ReadFile(cfg)
	existed := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return newStageError(CodeDockerWSLSSHNotReady, stageEndpoint, err)
	}
	if bytes.Contains(original, []byte(sshBlockBegin(spec.RunID))) {
		return configError("%s already holds a devsy-ci block for run %s", cfg, spec.RunID)
	}
	if err := d.backupSSHConfig(ctx, cfg, original, existed); err != nil {
		return err
	}
	if err := d.deps.Files.MkdirAll(filepath.Dir(cfg), stateDirPerm); err != nil {
		return newStageError(CodeDockerWSLSSHNotReady, stageEndpoint, err)
	}
	next := prependSSHBlock(original, sshConfigBlock(spec))
	if err := d.deps.Files.WriteFile(cfg, next, configFilePerm); err != nil {
		return newStageError(CodeDockerWSLSSHNotReady, stageEndpoint,
			fmt.Errorf("write %s: %w", cfg, err))
	}
	sum := sha256.Sum256(next)
	return d.record(
		func(s *dockerWSLState) { s.Owned.SSHConfigWritten = hex.EncodeToString(sum[:]) },
	)
}

func (d *dockerWSL) backupSSHConfig(
	ctx context.Context,
	cfg string,
	original []byte,
	existed bool,
) error {
	fx := d.guard.fixture()
	backup := filepath.Join(fx, sshConfigBackupNm)
	aclBackup := ""
	if existed {
		aclBackup = filepath.Join(fx, sshConfigACLNm)
		if err := d.deps.Files.WriteFile(backup, original, configFilePerm); err != nil {
			return newStageError(CodeDockerWSLSSHNotReady, stageEndpoint, err)
		}
		if err := d.saveACL(ctx, cfg, aclBackup); err != nil {
			return err
		}
	}
	return d.record(func(s *dockerWSLState) {
		s.Owned.SSHConfig, s.Owned.SSHConfigExisted = cfg, existed
		s.Owned.SSHConfigBackup, s.Owned.SSHConfigACLBackup = backup, aclBackup
		if !existed {
			s.Owned.SSHConfigBackup = ""
		}
	})
}

func (d *dockerWSL) saveACL(ctx context.Context, cfg, aclBackup string) error {
	icacls, err := d.tool(toolICACLS)
	if err != nil {
		return err
	}
	_, err = d.run(ctx, stageEndpoint, CodeDockerWSLSSHNotReady, Command{
		Path: icacls, Args: []string{cfg, "/save", aclBackup}, Timeout: wslQuickTimeout,
	})
	return err
}

// verifySSHAuth authenticates through the alias with BatchMode, using the very
// config block, key and strict known_hosts the Docker CLI's ssh.exe will use.
func (d *dockerWSL) verifySSHAuth(ctx context.Context, alias string) error {
	ssh, err := d.tool(toolSSH)
	if err != nil {
		return err
	}
	cmd := Command{
		Path: ssh,
		Args: []string{
			"-o", "BatchMode=yes",
			"-o", fmt.Sprintf("ConnectTimeout=%d", int(sshConnectTimeout.Seconds())),
			alias, "true",
		},
		Timeout: sshConnectTimeout + 5*time.Second,
	}
	var last *StageError
	for i := range sshAuthAttempts {
		probeErr, permanent := d.probeSSH(ctx, cmd)
		if probeErr == nil {
			return nil
		}
		last = probeErr
		if permanent || !d.pause(ctx, i, sshAuthAttempts, sshAuthGap) {
			break
		}
	}
	return d.sshDiagnostics(ctx, ssh, alias, last)
}

// probeSSH runs one authentication probe; a nil error means it succeeded.
func (d *dockerWSL) probeSSH(ctx context.Context, cmd Command) (*StageError, bool) {
	res, runErr := d.deps.Run.Run(ctx, withLimit(ctx, cmd))
	if runErr == nil && !res.TimedOut && res.ExitCode == 0 {
		return nil, false
	}
	output := res.Stderr + res.Stdout
	if runErr != nil {
		output += "\n" + runErr.Error()
	}
	code, permanent := classifySSHFailure(output)
	failure := newStageError(code, stageEndpoint, errors.New("ssh authentication probe failed"))
	return failure.WithDetail(output), permanent
}

// pause sleeps between attempts and reports whether another attempt may run.
func (d *dockerWSL) pause(ctx context.Context, attempt, attempts int, gap time.Duration) bool {
	if attempt >= attempts-1 || ctx.Err() != nil {
		return false
	}
	return d.deps.Clock.Sleep(ctx, gap) == nil
}

func withLimit(ctx context.Context, cmd Command) Command {
	cmd.Timeout = boundedTimeout(ctx, cmd.Timeout)
	return cmd
}

// classifySSHFailure maps ssh.exe output to a stage code and whether retrying
// can help. Authentication and host-key failures are permanent.
func classifySSHFailure(output string) (Code, bool) {
	switch {
	case strings.Contains(output, "Permission denied"):
		return CodeDockerWSLSSHAuth, true
	case strings.Contains(output, "Host key verification failed"),
		strings.Contains(output, "REMOTE HOST IDENTIFICATION HAS CHANGED"):
		return CodeDockerWSLEndpoint, true
	}
	return CodeDockerWSLSSHNotReady, false
}

// sshDiagnostics adds a verbose ssh transcript (no private material) and the
// sshd log tail to the failure.
func (d *dockerWSL) sshDiagnostics(ctx context.Context, ssh, alias string, last error) error {
	diagCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), wslQuickTimeout)
	defer cancel()
	verbose, _ := d.deps.Run.Run(diagCtx, Command{
		Path:    ssh,
		Args:    []string{"-vv", "-o", "BatchMode=yes", alias, "true"},
		Timeout: sshConnectTimeout + 5*time.Second,
	})
	se, ok := errors.AsType[*StageError](last)
	if !ok {
		se = newStageError(CodeDockerWSLSSHNotReady, stageEndpoint, last)
	}
	logs, _ := d.script(diagCtx, "diagnostics", se.Code,
		wslScript{body: diagnosticsScript, timeout: wslQuickTimeout})
	_ = se.WithDetail(se.Detail + "\n" + tailBytes(verbose.Stderr, 2<<10) + "\n" + logs.Stdout)
	return se
}

// verifyOracles proves the SSH route reaches the WSL daemon, no TCP API exists
// and the process-default pipe has no daemon. Only then are values exported.
func (d *dockerWSL) verifyOracles(ctx context.Context) error {
	endpoint := d.state.SSHEndpoint
	res, err := d.run(ctx, stageOracles, CodeDockerWSLEndpoint,
		d.dockerCommand(endpoint, "info", "--format", "{{.ID}}"))
	if err != nil {
		return err
	}
	sshID, err := parseDaemonID(res.Stdout)
	if err != nil {
		return newStageError(CodeDockerWSLEndpoint, stageOracles, err)
	}
	wslID, err := d.wslDaemonID(ctx, stageOracles)
	if err != nil {
		return err
	}
	if err := compareDaemonIDs(wslID, sshID); err != nil {
		return newStageError(CodeDockerWSLEndpoint, stageOracles, err)
	}
	if err := d.requireNoDockerTCP(ctx, stageOracles); err != nil {
		return err
	}
	dead := d.dockerCommand(deadDockerHost, "info")
	deadRes, runErr := d.deps.Run.Run(ctx, withLimit(ctx, dead))
	if err := requireDeadPipeFailure(deadRes, runErr); err != nil {
		return newStageError(CodeDockerWSLOracle, stageOracles, err)
	}
	return d.export(wslID)
}

// export publishes the verified fixture to later workflow steps.
func (d *dockerWSL) export(daemonID string) error {
	o := d.state.Owned
	exports := [][2]string{
		{"DOCKER_CONFIG", o.DockerConfigDir},
		{envSSHDockerHost, d.state.SSHEndpoint},
		{envWSLDaemonID, daemonID},
	}
	if err := d.gh.AddPath(o.DockerBinDirectory); err != nil {
		return err
	}
	for _, kv := range exports {
		if err := d.gh.SetEnv(kv[0], kv[1]); err != nil {
			return err
		}
	}
	return d.record(func(s *dockerWSLState) { s.Stage = stageVerified })
}
