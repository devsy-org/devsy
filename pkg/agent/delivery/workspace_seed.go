package delivery

import (
	"context"
	"fmt"
	"strings"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
)

// WorkspaceSeedOptions describes a request to seed a named workspace volume
// from a local source directory.
type WorkspaceSeedOptions struct {
	// WorkspaceID owns the volume (used for labels).
	WorkspaceID string
	// VolumeName is the named volume backing the workspace mount.
	VolumeName string
	// SourceDir is the host/remote directory whose contents are copied into
	// the volume (a faithful working-tree copy).
	SourceDir string
	// Reset removes an existing managed volume first so it is re-seeded.
	Reset bool
}

// WorkspaceVolumeSeeder is implemented by deliveries that can populate a named
// workspace volume from a local directory.
type WorkspaceVolumeSeeder interface {
	SeedWorkspaceVolume(ctx context.Context, opts WorkspaceSeedOptions) error
}

var _ WorkspaceVolumeSeeder = (*LocalDockerDelivery)(nil)

// SeedWorkspaceVolume ensures the workspace volume exists and, unless it was
// already seeded, copies SourceDir into it as a faithful working-tree copy.
// Seeding is idempotent: an already-seeded volume is left untouched unless
// Reset is set.
func (d *LocalDockerDelivery) SeedWorkspaceVolume(
	ctx context.Context,
	opts WorkspaceSeedOptions,
) error {
	if opts.VolumeName == "" || opts.SourceDir == "" {
		return fmt.Errorf("volume name and source dir are required")
	}

	proceed, err := d.prepareSeedTarget(ctx, opts)
	if err != nil {
		return err
	}
	if !proceed {
		return nil
	}

	labels := pkgconfig.DockerVolumeLabels(opts.WorkspaceID, pkgconfig.VolumeRoleWorkspace)
	if err := d.createVolume(ctx, opts.VolumeName, labels); err != nil {
		return fmt.Errorf("create workspace volume: %w", err)
	}

	if err := d.copyDirIntoVolume(ctx, opts.SourceDir, opts.VolumeName); err != nil {
		// Leave the volume in place but unseeded so a retry re-copies.
		return fmt.Errorf("seed workspace volume: %w", err)
	}

	if err := d.markVolumeSeeded(ctx, opts.VolumeName); err != nil {
		log.Debugf("failed to mark volume %s seeded: %v", opts.VolumeName, err)
	}
	log.Infof("seeded workspace volume %s from %s", opts.VolumeName, opts.SourceDir)
	return nil
}

// prepareSeedTarget decides whether seeding should proceed and performs any
// required reset. It returns false (without error) when seeding should be
// skipped: the volume is external (present but not devsy-managed) or already
// seeded and no reset was requested.
func (d *LocalDockerDelivery) prepareSeedTarget(
	ctx context.Context,
	opts WorkspaceSeedOptions,
) (proceed bool, err error) {
	managed, seeded, err := d.volumeSeedState(ctx, opts.VolumeName)
	if err != nil {
		return false, err
	}

	// A pre-existing volume that devsy does not manage is treated as external:
	// never overwrite user-provided content.
	if !managed && d.volumeExists(ctx, opts.VolumeName) {
		log.Debugf("workspace volume %s is not devsy-managed; skipping seed", opts.VolumeName)
		return false, nil
	}

	if opts.Reset && managed {
		log.Warnf(
			"--reset: removing workspace volume %s and re-seeding from source; "+
				"any changes made inside the volume will be lost",
			opts.VolumeName,
		)
		if err := d.removeVolume(ctx, opts.VolumeName); err != nil {
			return false, fmt.Errorf("reset workspace volume: %w", err)
		}
		return true, nil
	}

	if seeded {
		log.Debugf("workspace volume %s already seeded; skipping", opts.VolumeName)
		return false, nil
	}
	return true, nil
}

// volumeSeedState reports whether the volume is devsy-managed (from its label)
// and whether it has already been seeded (from a sentinel file written inside
// the volume after a successful copy). Docker cannot add labels to an existing
// volume, so seeded state is tracked with the sentinel rather than a label.
func (d *LocalDockerDelivery) volumeSeedState(
	ctx context.Context,
	name string,
) (managed, seeded bool, err error) {
	if !d.volumeExists(ctx, name) {
		return false, false, nil
	}

	out, err := d.cmd(ctx,
		"volume", "inspect",
		"--format", "{{index .Labels \""+pkgconfig.DockerManagedLabel+"\"}}",
		name,
	).CombinedOutput()
	if err != nil {
		return false, false, fmt.Errorf("inspect volume %s: %s: %w", name, string(out), err)
	}
	managed = strings.TrimSpace(string(out)) == "true"

	return managed, d.volumeSeeded(ctx, name), nil
}

// volumeSeeded reports whether the seeded sentinel file exists in the volume.
func (d *LocalDockerDelivery) volumeSeeded(ctx context.Context, name string) bool {
	args := []string{
		cmdRun, flagRM,
		"-v", name + ":/target:ro",
		d.helperImageName(),
		"sh", "-c", "[ -e /target/" + seededSentinel + " ]",
	}
	return d.cmd(ctx, args...).Run() == nil
}

func (d *LocalDockerDelivery) volumeExists(ctx context.Context, name string) bool {
	err := d.cmd(ctx, "volume", "inspect", name).Run()
	return err == nil
}

// copyDirIntoVolume copies the contents of sourceDir into the volume root using
// a throwaway helper container. The source is bind-mounted read-only.
func (d *LocalDockerDelivery) copyDirIntoVolume(
	ctx context.Context,
	sourceDir, volumeName string,
) error {
	args := []string{
		cmdRun, flagRM,
		"-v", sourceDir + ":/source:ro",
		"-v", volumeName + ":/target",
		d.helperImageName(),
		"sh", "-c", "cp -a /source/. /target/",
	}
	out, err := d.cmd(ctx, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", string(out), err)
	}
	return nil
}

// markVolumeSeeded records that the volume has been populated by writing a
// sentinel file inside it. Docker cannot add labels to an existing volume, so
// the sentinel is the source of truth for seeded state.
func (d *LocalDockerDelivery) markVolumeSeeded(ctx context.Context, volumeName string) error {
	args := []string{
		cmdRun, flagRM,
		"-v", volumeName + ":/target",
		d.helperImageName(),
		"sh", "-c", "touch /target/" + seededSentinel,
	}
	out, err := d.cmd(ctx, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", string(out), err)
	}
	return nil
}

const seededSentinel = ".devsy-seeded"
