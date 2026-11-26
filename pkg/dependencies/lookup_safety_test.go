package dependencies

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	lookupTestLocalVolume      = `\Device\HarddiskVolume7`
	lookupTestStatCall         = "stat"
	lookupTestReviewedLocation = "reviewed"
)

// Synthetic boundary tests run on every host; they do not exercise Windows syscalls.
func TestWindowsLookupRejectsBeforeMetadata(t *testing.T) {
	for _, path := range []string{
		`\\server\share\docker.exe`, `\\?\UNC\server\share\docker.exe`,
		`\\.\pipe\docker`, `\\?\C:\docker.exe`, `\??\C:\docker.exe`,
		`C:docker.exe`, `\docker.exe`, `C:\safe\..\docker.exe`,
		`C:\safe\docker.exe:stream`, `C:\safe\NUL.exe`,
	} {
		t.Run(path, func(t *testing.T) {
			metadata := &lookupMetadataSpy{t: t, forbidCalls: true}
			observed, err := inspectWindowsLookup(context.Background(), path, metadata)
			require.ErrorIs(t, err, errLookupDeferred)
			require.Nil(t, observed.info)
			require.Empty(t, metadata.calls)
		})
	}
}

func TestWindowsLookupRejectsMappedAndUnsupportedVolumesBeforeOpen(t *testing.T) {
	for _, target := range []string{
		`\Device\Mup\server\share`, `\??\UNC\server\share`, `\??\C:\alias`,
		`\Device\HarddiskVolume1\alias`, `\Device\CdRom0`, `\Device\HarddiskVolume`,
	} {
		metadata := &lookupMetadataSpy{t: t, target: target}
		_, err := inspectWindowsLookup(context.Background(), `Z:\bin\docker.exe`, metadata)
		require.ErrorIs(t, err, errLookupDeferred)
		require.Equal(t, []string{"drive Z:"}, metadata.calls)
	}
}

func TestWindowsLookupStopsAtLocalParentReparse(t *testing.T) {
	// The synthetic reparse represents a junction/link to a remote target. The
	// boundary neither reads that target nor opens a child of the reparse handle.
	for _, at := range []uintptr{1, 2, 3, 4} {
		metadata := &lookupMetadataSpy{t: t, target: lookupTestLocalVolume, reparseAt: at}
		observed, err := inspectWindowsLookup(
			context.Background(),
			`C:\safe\link\docker.exe`,
			metadata,
		)
		require.ErrorIs(t, err, errLookupDeferred)
		require.True(t, observed.symlink)
		require.Nil(t, observed.info)
		require.Empty(t, observed.resolved)
		require.Equal(t, at, metadata.opens)
		require.Empty(t, metadata.live)
	}
}

func TestWindowsLookupLocalHandleWalk(t *testing.T) {
	metadata := &lookupMetadataSpy{t: t, target: lookupTestLocalVolume}
	path := `C:\safe\bin\docker.exe`
	observed, err := inspectWindowsLookup(context.Background(), path, metadata)
	require.NoError(t, err)
	require.NotNil(t, observed.info)
	require.Equal(t, path, observed.resolved)
	require.False(t, observed.symlink)
	require.Equal(t, uintptr(4), metadata.opens)
	require.Empty(t, metadata.live)
	require.Equal(t, []string{
		"drive C:",
		`open \Device\HarddiskVolume7\`,
		lookupTestStatCall,
		"open safe",
		lookupTestStatCall,
		"open bin",
		lookupTestStatCall,
		"open docker.exe",
		lookupTestStatCall,
	}, metadata.calls)
}

func TestWindowsLookupClosesHandlesOnFailure(t *testing.T) {
	for _, failure := range []string{"open", lookupTestStatCall} {
		metadata := &lookupMetadataSpy{t: t, target: lookupTestLocalVolume, failure: failure}
		_, err := inspectWindowsLookup(context.Background(), `C:\safe\docker.exe`, metadata)
		require.Error(t, err)
		require.Empty(t, metadata.live)
	}
}

type lookupMetadataSpy struct {
	t           *testing.T
	forbidCalls bool
	target      string
	calls       []string
	opens       uintptr
	reparseAt   uintptr
	live        map[uintptr]bool
	failure     string
	afterCall   func()
}

func (s *lookupMetadataSpy) record(call string) {
	s.t.Helper()
	require.False(s.t, s.forbidCalls, "forbidden metadata call: %s", call)
	s.calls = append(s.calls, call)
	if s.afterCall != nil {
		s.afterCall()
	}
}

func (s *lookupMetadataSpy) driveTarget(drive string) (string, error) {
	s.record("drive " + drive)
	return s.target, nil
}

func (s *lookupMetadataSpy) open(parent uintptr, name string) (uintptr, error) {
	s.record("open " + name)
	if parent != 0 {
		require.True(s.t, s.live[parent])
		require.NotEqual(s.t, s.reparseAt, parent, "must never traverse a reparse handle")
		require.NotContains(s.t, name, `\`)
	}
	if s.failure == "open" {
		return 0, errors.New("synthetic open failure")
	}
	if s.live == nil {
		s.live = make(map[uintptr]bool)
	}
	s.opens++
	s.live[s.opens] = true
	return s.opens, nil
}

func (s *lookupMetadataSpy) stat(handle uintptr) (fs.FileInfo, bool, error) {
	s.record(lookupTestStatCall)
	require.True(s.t, s.live[handle])
	if s.failure == lookupTestStatCall {
		return nil, false, errors.New("synthetic stat failure")
	}
	return lookupSyntheticInfo{name: "entry", directory: handle < 4}, handle == s.reparseAt, nil
}

func (s *lookupMetadataSpy) close(handle uintptr) {
	require.True(s.t, s.live[handle])
	delete(s.live, handle)
}

type lookupSyntheticInfo struct {
	name      string
	directory bool
}

func (i lookupSyntheticInfo) Name() string { return i.name }
func (i lookupSyntheticInfo) Size() int64  { return 0 }
func (i lookupSyntheticInfo) Mode() fs.FileMode {
	if i.directory {
		return fs.ModeDir | 0o700
	}
	return 0o700
}
func (i lookupSyntheticInfo) ModTime() time.Time { return time.Time{} }
func (i lookupSyntheticInfo) IsDir() bool        { return i.directory }
func (i lookupSyntheticInfo) Sys() any           { return nil }

func TestWindowsLookupComponentPolicy(t *testing.T) {
	for _, part := range []string{".", "..", "COM1.exe", "LPT9", "trailing ", "trailing.", "bad\x00name"} {
		require.False(t, windowsLookupComponent(part), part)
	}
	for _, part := range []string{" spaced dir", "docker tool.exe", "snow-\u96ea.exe", "COM10.exe"} {
		require.True(t, windowsLookupComponent(part), part)
	}
	require.True(t, localWindowsVolume(`\Device\HarddiskVolume123`))
	require.False(t, localWindowsVolume(strings.Repeat("x", 30)))
}

func TestLookupCancellationDuringPATHPreflight(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, cancelAt := range []int{1, 2} {
			t.Run(fmt.Sprintf("explicit=%t/cancelAt=%d", explicit, cancelAt), func(t *testing.T) {
				root := t.TempDir()
				paths := []string{root}
				if cancelAt == 2 {
					paths = []string{filepath.Join(root, "first"), filepath.Join(root, "second")}
					for _, path := range paths {
						require.NoError(t, os.Mkdir(path, 0o700))
					}
				}
				environment, err := NewLookupEnvironment(map[string]string{
					"PATH": strings.Join(paths, string(filepath.ListSeparator)),
				}, root)
				require.NoError(t, err)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var reads []string
				environment.inspect = func(ctx context.Context, path string) (lookupFile, error) {
					reads = append(reads, path)
					observed, inspectErr := inspectLookupFile(ctx, path)
					if len(reads) == cancelAt {
						cancel()
					}
					return observed, inspectErr
				}
				result, err := environment.LookupExecutable(
					ctx,
					LookupRequest{Command: toolDocker, Explicit: explicit},
				)
				require.Equal(t, context.Canceled, err)
				require.Empty(t, result)
				require.Len(t, reads, cancelAt, "no metadata read after cancellation")
			})
		}
	}
}

func TestWindowsLookupCancellationAtMetadataBoundaries(t *testing.T) {
	for cancelAt := 1; cancelAt <= 9; cancelAt++ {
		t.Run(fmt.Sprint(cancelAt), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			metadata := &lookupMetadataSpy{t: t, target: lookupTestLocalVolume}
			metadata.afterCall = func() {
				if len(metadata.calls) == cancelAt {
					cancel()
				}
			}
			observed, err := inspectWindowsLookup(ctx, `C:\safe\bin\docker.exe`, metadata)
			require.Equal(t, context.Canceled, err)
			require.Empty(t, observed)
			require.Len(t, metadata.calls, cancelAt, "no metadata operation after cancellation")
			require.Empty(t, metadata.live, "all acquired handles closed")
		})
	}
}

func TestLookupPreservesCancellationErrors(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.Mkdir(bin, 0o700))
	for _, failure := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, scenario := range []struct {
			name    string
			at      int
			request LookupRequest
		}{
			{name: "cwd", at: 1, request: LookupRequest{Command: toolDocker}},
			{name: "directory", at: 2, request: LookupRequest{Command: toolDocker}},
			{name: "candidate", at: 3, request: LookupRequest{Command: toolDocker}},
			{name: "explicit", at: 1, request: LookupRequest{Command: filepath.Join(bin, "docker.exe"), Explicit: true}},
			{name: lookupTestReviewedLocation, at: 1, request: LookupRequest{
				Command: toolDocker, ReviewedLocations: []string{filepath.Join(bin, "docker.exe")},
			}},
		} {
			t.Run(failure.Error()+"/"+scenario.name, func(t *testing.T) {
				values := map[string]string{"PATH": bin}
				if scenario.name == lookupTestReviewedLocation {
					values = nil
				}
				environment, err := NewLookupEnvironment(values, root)
				require.NoError(t, err)
				reads := 0
				environment.inspect = func(ctx context.Context, path string) (lookupFile, error) {
					reads++
					if reads == scenario.at {
						return lookupFile{}, failure
					}
					return inspectLookupFile(ctx, path)
				}
				result, err := environment.LookupExecutable(context.Background(), scenario.request)
				require.Equal(t, failure, err)
				require.Empty(t, result)
				require.Equal(t, scenario.at, reads)
			})
		}
	}
}
