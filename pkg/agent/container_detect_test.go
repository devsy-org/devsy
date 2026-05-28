package agent

import (
	"errors"
	"testing"
)

var (
	noFiles statExistsFn = func(string) (bool, error) { return false, nil }
	noRead  readFileFn   = func(string) ([]byte, error) { return nil, nil }
)

type containerCase struct {
	name string
	stat statExistsFn
	read readFileFn
	want bool
}

func markerCases() []containerCase {
	return []containerCase{
		{
			name: "host: no markers, no cgroup",
			stat: noFiles, read: noRead, want: false,
		},
		{
			name: "container: /.dockerenv present",
			stat: func(p string) (bool, error) { return p == dockerEnvPath, nil },
			read: noRead, want: true,
		},
		{
			name: "container: /run/.containerenv present (podman)",
			stat: func(p string) (bool, error) { return p == podmanEnvPath, nil },
			read: noRead, want: true,
		},
		{
			name: "host: stat error on marker treated as absent",
			stat: func(p string) (bool, error) { return false, errors.New("perm") },
			read: noRead, want: false,
		},
	}
}

func cgroupCases() []containerCase {
	return []containerCase{
		{
			name: "container: cgroup contains docker",
			stat: noFiles,
			read: func(p string) ([]byte, error) {
				if p == cgroupPath {
					return []byte("12:cpu:/docker/abcdef\n"), nil
				}
				return nil, nil
			},
			want: true,
		},
		{
			name: "container: cgroup contains containerd",
			stat: noFiles,
			read: func(p string) ([]byte, error) {
				if p == cgroupPath {
					return []byte("0::/system.slice/containerd.service\n"), nil
				}
				return nil, nil
			},
			want: true,
		},
		{
			name: "host: cgroup present but no container token",
			stat: noFiles,
			read: func(string) ([]byte, error) {
				return []byte("0::/user.slice/user-1000.slice\n"), nil
			},
			want: false,
		},
		{
			name: "host: cgroup unreadable (ENOENT swallowed)",
			stat: noFiles, read: noRead, want: false,
		},
		{
			name: "host: cgroup read error propagates as host",
			stat: noFiles,
			read: func(string) ([]byte, error) { return nil, errors.New("eio") },
			want: false,
		},
	}
}

func runContainerCases(t *testing.T, cases []containerCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isLikelyContainerWith(tc.stat, tc.read)
			if got != tc.want {
				t.Fatalf("isLikelyContainerWith = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsLikelyContainer_Markers(t *testing.T) {
	runContainerCases(t, markerCases())
}

func TestIsLikelyContainer_Cgroup(t *testing.T) {
	runContainerCases(t, cgroupCases())
}

// TestDefaultStatExists_NotFound ensures the production stat wrapper
// treats ENOENT as a non-error "no" instead of bubbling up.
func TestDefaultStatExists_NotFound(t *testing.T) {
	ok, err := defaultStatExists("/definitely/not/a/real/path/devsy-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for non-existent path")
	}
}

// TestDefaultReadFile_NotFound ensures the production reader treats
// ENOENT as a non-error empty read.
func TestDefaultReadFile_NotFound(t *testing.T) {
	b, err := defaultReadFile("/definitely/not/a/real/path/devsy-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b != nil {
		t.Fatalf("expected nil bytes, got %q", string(b))
	}
}
