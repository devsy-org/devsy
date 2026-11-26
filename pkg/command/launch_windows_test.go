//go:build windows

package command

import (
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestAtomicLaunchRejectsExistingJobWithoutClaimingIt(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	const name = "devsy-launch-collision-test"
	job, _, err := createLaunchJob(name)
	require.NoError(t, err)
	t.Cleanup(func() { _ = windows.CloseHandle(job) })
	worker := startHelper(t)
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(worker.Process.Pid),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = windows.CloseHandle(process) })
	require.NoError(t, windows.AssignProcessToJobObject(job, process))
	cmd := exec.Command(
		os.Args[0],
		"-test.run=^TestAtomicLaunchRejectsExistingJobWithoutClaimingIt$",
	) // #nosec G204 G702 -- re-exec this test binary
	prepared := false
	_, err = startInWorkerJob(cmd, name, func(string) error { prepared = true; return nil })
	require.ErrorContains(t, err, "already exists")
	require.False(t, prepared)
	require.Nil(t, cmd.Process)
	require.NoError(t, CleanupUnpublishedLaunch("", name, prepared))
	running, err := IsRunning(strconv.Itoa(worker.Process.Pid))
	require.NoError(t, err)
	require.True(t, running, "a failed ownership claim must not terminate the existing job")
}

func TestLaunchJobPublicationKeepsCreatedIdentity(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	job, jobID, err := createLaunchJob("devsy-launch-publication-test")
	require.NoError(t, err)
	t.Cleanup(func() { _ = windows.CloseHandle(job) })
	t.Setenv(config.EnvHome, t.TempDir())
	var published string
	require.NoError(t, notifyLaunchPrepared(func(id string) error {
		published = id
		return nil
	}, jobID))
	owned, found, err := openNamedJob(published, published)
	require.NoError(t, err)
	require.True(
		t,
		found,
		"publication must identify the created job after the home directory changes",
	)
	t.Cleanup(func() { _ = windows.CloseHandle(owned) })
}
