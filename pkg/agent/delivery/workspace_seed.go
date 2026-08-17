package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
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
	labels[pkgconfig.DockerSeededLabel] = pkgconfig.LabelValueTrue
	if err := d.createVolume(ctx, opts.VolumeName, labels); err != nil {
		return fmt.Errorf("create workspace volume: %w", err)
	}

	if err := d.copyDirIntoVolume(ctx, opts.SourceDir, opts.VolumeName); err != nil {
		if rmErr := d.removeVolume(ctx, opts.VolumeName); rmErr != nil {
			return errors.Join(
				fmt.Errorf("seed workspace volume: %w", err),
				fmt.Errorf("remove partial volume %s: %w", opts.VolumeName, rmErr),
			)
		}
		return fmt.Errorf("seed workspace volume: %w", err)
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
			"%s: removing workspace volume %s and re-seeding from source; "+
				"any changes made inside the volume will be lost",
			names.Flag(names.Reset),
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

func (d *LocalDockerDelivery) volumeSeedState(
	ctx context.Context,
	name string,
) (managed, seeded bool, err error) {
	if !d.volumeExists(ctx, name) {
		return false, false, nil
	}

	out, err := d.cmd(ctx,
		"volume", "inspect",
		"--format", "{{index .Labels \""+pkgconfig.DockerManagedLabel+"\"}},"+
			"{{index .Labels \""+pkgconfig.DockerSeededLabel+"\"}}",
		name,
	).CombinedOutput()
	if err != nil {
		return false, false, fmt.Errorf("inspect volume %s: %s: %w", name, string(out), err)
	}
	managedStr, seededStr, _ := strings.Cut(strings.TrimSpace(string(out)), ",")
	return managedStr == pkgconfig.LabelValueTrue, seededStr == pkgconfig.LabelValueTrue, nil
}

func (d *LocalDockerDelivery) volumeExists(ctx context.Context, name string) bool {
	err := d.cmd(ctx, "volume", "inspect", name).Run()
	return err == nil
}

func (d *LocalDockerDelivery) copyDirIntoVolume(
	ctx context.Context,
	sourceDir, volumeName string,
) error {
	var script strings.Builder
	script.WriteString("cd /source && tar -c")
	for _, artifact := range config.BuildArtifactExcludes() {
		script.WriteString(" --exclude='")
		script.WriteString(artifact)
		script.WriteString("'")
	}
	script.WriteString(" . | tar -x -C /target")
	args := []string{
		cmdRun, flagRM,
		"-v", sourceDir + ":/source:ro",
		"-v", volumeName + ":/target",
		d.helperImageName(),
		"sh", "-c", script.String(),
	}
	out, err := d.cmd(ctx, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", string(out), err)
	}
	return nil
}
