package dockerwsl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/devsy-org/devsy/e2e/framework/runenv"
)

const (
	stageCleanup = "cleanup"

	stepRestoreSSHConfig = "restore-ssh-config"
	stepStopSSHD         = "stop-sshd"
	stepRemoveUser       = "remove-ci-user"
	stepStopDockerd      = "stop-dockerd"
	stepStopInstaller    = "stop-installer-daemon"
	stepRemoveLinuxFiles = "remove-linux-files"
	stepVerifyNoTCP      = "verify-no-docker-tcp"
	stepRemoveFixture    = "remove-windows-fixture"
)

type cleanupStep struct {
	name   string
	needed bool
	// requires names steps that must have succeeded before this one runs.
	requires []string
	run      func(context.Context) error
}

// cleanup tears the fixture down in reverse acquisition order using only the
// validated state file. A missing state file is a no-op; every step is
// idempotent; any step failure fails the command.
func (d *dockerWSL) cleanup(ctx context.Context) error {
	st, err := d.store.load()
	if errors.Is(err, errStateMissing) {
		_, _ = fmt.Fprintf(
			d.deps.Out,
			"no docker-wsl state at %s; nothing to clean up\n",
			d.opts.StateFile,
		)
		return nil
	}
	if err != nil {
		return err
	}
	if err := d.guard.validate(st); err != nil {
		return err
	}
	d.state = st
	ctx, cancel := context.WithTimeout(ctx, d.opts.Timeout)
	defer cancel()
	artifact := d.collectDiagnostics(ctx, "cleanup")
	failures := d.runCleanupSteps(ctx)
	if len(failures) > 0 {
		saveErr := d.store.save(d.state)
		err := runenv.NewStageError(runenv.CodeDockerWSLCleanup, stageCleanup,
			errors.Join(append(failures, saveErr)...)).WithArtifact(artifact)
		return err
	}
	return d.store.remove()
}

func (d *dockerWSL) cleanupSteps() []cleanupStep {
	o := d.state.Owned
	processes := []string{stepStopSSHD, stepRemoveUser, stepStopDockerd, stepStopInstaller}
	return []cleanupStep{
		{name: stepRestoreSSHConfig, needed: o.SSHConfig != "", run: d.restoreSSHConfig},
		{name: stepStopSSHD, needed: o.SSHDPidfile != "", run: d.stopSSHD},
		{name: stepRemoveUser, needed: o.CIUser != "", run: d.removeUser},
		{name: stepStopDockerd, needed: o.DockerPidfile != "", run: d.stopDockerd},
		{name: stepStopInstaller, needed: o.InstallerDaemon, run: d.stopInstallerDaemon},
		{
			name: stepRemoveLinuxFiles, needed: d.hasLinuxResources(),
			requires: processes, run: d.removeLinuxFiles,
		},
		{
			name: stepVerifyNoTCP, needed: o.DockerPidfile != "" || o.InstallerDaemon,
			requires: []string{stepStopDockerd, stepStopInstaller}, run: d.verifyNoDockerTCP,
		},
		{
			name: stepRemoveFixture, needed: d.hasWindowsResources(),
			requires: []string{stepRestoreSSHConfig}, run: d.removeFixture,
		},
	}
}

func (d *dockerWSL) runCleanupSteps(ctx context.Context) []error {
	var failures []error
	failed := map[string]bool{}
	for _, step := range d.cleanupSteps() {
		if !step.needed {
			continue
		}
		err := d.runCleanupStep(ctx, step, failed)
		d.recordTeardown(step.name, err)
		if err != nil {
			failed[step.name] = true
			failures = append(failures, fmt.Errorf("%s: %w", step.name, err))
		}
	}
	return failures
}

// runCleanupStep skips a step whose prerequisites failed: deleting the backup
// after a failed restore, or the account files while its daemon still runs,
// would destroy the only way to finish the job.
func (d *dockerWSL) runCleanupStep(
	ctx context.Context,
	step cleanupStep,
	failed map[string]bool,
) error {
	for _, dep := range step.requires {
		if failed[dep] {
			return fmt.Errorf("skipped: prerequisite %s failed", dep)
		}
	}
	return step.run(ctx)
}

// recordTeardown keeps the attempt count and last error per step in the state.
func (d *dockerWSL) recordTeardown(step string, err error) {
	msg := ""
	if err != nil {
		msg = truncateError(err)
	}
	for i := range d.state.Teardown {
		if d.state.Teardown[i].Step == step {
			d.state.Teardown[i].Attempts++
			d.state.Teardown[i].Error = msg
			return
		}
	}
	d.state.Teardown = append(d.state.Teardown, teardownRecord{Step: step, Attempts: 1, Error: msg})
}

func (d *dockerWSL) hasWindowsResources() bool {
	o := d.state.Owned
	return o.SSHIdentityFile != "" || o.KnownHostsFile != "" || o.DockerCLIDirectory != "" ||
		o.DockerConfigDir != "" || o.SSHConfigBackup != "" || o.SSHConfigACLBackup != ""
}

func (d *dockerWSL) stopOwned(ctx context.Context, pidfile, name, marker string) error {
	_, err := d.script(ctx, stageCleanup, runenv.CodeDockerWSLCleanup, wslScript{
		body: stopOwnedScript, args: []string{pidfile, name, marker}, timeout: wslInstallTimeout,
	})
	return err
}

func (d *dockerWSL) stopSSHD(ctx context.Context) error {
	return d.stopOwned(ctx, sshdPidfile, "sshd", sshdMarker)
}

func (d *dockerWSL) stopDockerd(ctx context.Context) error {
	return d.stopOwned(ctx, dockerPidfile, "dockerd", dockerdMarker)
}

func (d *dockerWSL) stopInstallerDaemon(ctx context.Context) error {
	_, err := d.script(ctx, stageCleanup, runenv.CodeDockerWSLCleanup,
		wslScript{body: stopInstallerDaemonScript, timeout: wslScriptTimeout})
	return err
}

func (d *dockerWSL) removeUser(ctx context.Context) error {
	_, err := d.script(ctx, stageCleanup, runenv.CodeDockerWSLCleanup, wslScript{
		body: removeUserScript, args: []string{ciUser, userMarker(d.state.RunID)},
		timeout: wslScriptTimeout,
	})
	return err
}

func (d *dockerWSL) removeLinuxFiles(ctx context.Context) error {
	_, err := d.script(ctx, stageCleanup, runenv.CodeDockerWSLCleanup,
		wslScript{body: removeFilesScript, timeout: wslScriptTimeout})
	return err
}

func (d *dockerWSL) verifyNoDockerTCP(ctx context.Context) error {
	return d.requireNoDockerTCP(ctx, stageCleanup)
}

// removeFixture deletes the run's Windows fixture tree, the only tree the
// validated state can name. The state directory itself and the diagnostics
// file stay.
func (d *dockerWSL) removeFixture(context.Context) error {
	if err := d.deps.Files.RemoveAll(d.guard.fixture()); err != nil {
		return fmt.Errorf("remove %s: %w", d.guard.fixture(), err)
	}
	return nil
}

// restoreSSHConfig undoes the prepended block. When the file is exactly what
// setup wrote, the backed-up bytes are restored verbatim; otherwise only this
// run's block is cut out, leaving later edits alone.
func (d *dockerWSL) restoreSSHConfig(ctx context.Context) error {
	o := d.state.Owned
	current, err := d.deps.Files.ReadFile(o.SSHConfig)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", o.SSHConfig, err)
	}
	restored, changed, err := d.restoredConfig(current)
	if err != nil {
		return err
	}
	if !changed {
		// A retry after a failed ACL restore sees the config already restored.
		return d.restoreACL(ctx)
	}
	if o.SSHConfigExisted || len(restored) > 0 {
		err = d.deps.Files.WriteFile(o.SSHConfig, restored, configFilePerm)
	} else {
		err = d.deps.Files.Remove(o.SSHConfig)
	}
	if err != nil {
		return fmt.Errorf("restore %s: %w", o.SSHConfig, err)
	}
	return d.restoreACL(ctx)
}

// restoredConfig returns the bytes the config must hold after cleanup.
func (d *dockerWSL) restoredConfig(current []byte) ([]byte, bool, error) {
	o := d.state.Owned
	sum := sha256.Sum256(current)
	if o.SSHConfigWritten != "" && hex.EncodeToString(sum[:]) == o.SSHConfigWritten {
		if !o.SSHConfigExisted {
			return nil, true, nil
		}
		backup, err := d.deps.Files.ReadFile(o.SSHConfigBackup)
		if err != nil {
			return nil, false, fmt.Errorf("read ssh config backup: %w", err)
		}
		return backup, true, nil
	}
	stripped, found := stripSSHBlock(current, d.state.RunID)
	return stripped, found, nil
}

// restoreACL reapplies the ACLs saved before the config was replaced.
func (d *dockerWSL) restoreACL(ctx context.Context) error {
	o := d.state.Owned
	if o.SSHConfigACLBackup == "" || !o.SSHConfigExisted {
		return nil
	}
	icacls, err := d.tool(toolICACLS)
	if err != nil {
		return err
	}
	_, err = d.run(ctx, stageCleanup, runenv.CodeDockerWSLCleanup, runenv.Command{
		Path:    icacls,
		Args:    []string{filepath.Dir(o.SSHConfig), "/restore", o.SSHConfigACLBackup},
		Timeout: wslQuickTimeout,
	})
	return err
}
