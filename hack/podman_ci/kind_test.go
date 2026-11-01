package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	kindHelperEnv     = "DEVSY_PODMAN_KIND_HELPER"
	kindHelperExitEnv = "DEVSY_PODMAN_KIND_HELPER_EXIT"
	testKindImage     = "kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed"
	testClusterName   = "0123456789abcdef0123456789abcdef"
	testDockerHost    = "npipe:////./pipe/podman-machine-default"
)

func TestMain(m *testing.M) {
	if out := os.Getenv(kindHelperEnv); out != "" {
		os.Exit(runKindHelper(out))
	}
	os.Exit(m.Run())
}

type kindHelperReport struct {
	Args         []string `json:"args"`
	KindProvider string   `json:"kindExperimentalProvider"`
	DockerHost   string   `json:"dockerHost"`
}

func runKindHelper(out string) int {
	report := kindHelperReport{
		Args:         os.Args[1:],
		KindProvider: os.Getenv("KIND_EXPERIMENTAL_PROVIDER"),
		DockerHost:   os.Getenv("DOCKER_HOST"),
	}
	data, err := json.Marshal(report)
	if err != nil {
		return 2
	}
	if err := os.WriteFile(out, data, 0o600); err != nil {
		return 2
	}
	code, _ := strconv.Atoi(os.Getenv(kindHelperExitEnv))
	return code
}

type kindClusterCall struct{ kindPath, name, image string }

type kindFixture struct {
	deps      Dependencies
	cfg       KindConfig
	files     *installFS
	out       *bytes.Buffer
	downloads int
	clusters  []kindClusterCall
}

func newKindFixture(t *testing.T, kind []byte) *kindFixture {
	t.Helper()
	dir := t.TempDir()
	fx := &kindFixture{
		files: &installFS{files: map[string][]byte{}, appends: map[string]*installWriter{}},
		out:   &bytes.Buffer{},
	}
	fx.cfg = KindConfig{
		URL:        "https://example.test/kind-windows-amd64",
		SHA256:     installSum(kind),
		KindPath:   filepath.Join(dir, "kind.exe"),
		Image:      testKindImage,
		GitHubPath: filepath.Join(dir, "github_path"),
	}
	fx.deps = Dependencies{
		Log:   &Logger{Out: fx.out, Err: fx.out},
		Files: fx.files,
		Download: func(_ context.Context, _, dest string) error {
			fx.downloads++
			fx.files.files[dest] = kind
			return nil
		},
		NewClusterName: func() (string, error) { return testClusterName, nil },
		CreateCluster: func(_ context.Context, kindPath, name, image string) error {
			fx.clusters = append(fx.clusters, kindClusterCall{kindPath, name, image})
			return nil
		},
	}
	return fx
}

func (fx *kindFixture) gitHubPathContent(t *testing.T) string {
	t.Helper()
	w := fx.files.appends[fx.cfg.GitHubPath]
	if w == nil {
		t.Fatalf("no GITHUB_PATH append to %s", fx.cfg.GitHubPath)
	}
	return w.String()
}

func TestInstallKindSkipsDownloadWhenBinaryPresent(t *testing.T) {
	kind := []byte("kind binary")
	fx := newKindFixture(t, kind)
	fx.files.files[fx.cfg.KindPath] = kind
	fx.cfg.SHA256 = strings.ToUpper(fx.cfg.SHA256)

	if err := installKind(context.Background(), fx.deps, fx.cfg); err != nil {
		t.Fatal(err)
	}
	if fx.downloads != 0 {
		t.Fatalf("downloads = %d, want 0", fx.downloads)
	}
	want := []kindClusterCall{{fx.cfg.KindPath, testClusterName, testKindImage}}
	if !reflect.DeepEqual(fx.clusters, want) {
		t.Fatalf("clusters = %+v, want %+v", fx.clusters, want)
	}
	if got, want := fx.gitHubPathContent(t), filepath.Dir(fx.cfg.KindPath)+"\n"; got != want {
		t.Fatalf("GITHUB_PATH content = %q, want %q", got, want)
	}
}

func TestInstallKindDownloadsMissingBinary(t *testing.T) {
	kind := []byte("kind binary")
	fx := newKindFixture(t, kind)

	if err := installKind(context.Background(), fx.deps, fx.cfg); err != nil {
		t.Fatal(err)
	}
	if fx.downloads != 1 {
		t.Fatalf("downloads = %d, want 1", fx.downloads)
	}
	if got := string(fx.files.files[fx.cfg.KindPath]); got != string(kind) {
		t.Fatalf("downloaded content = %q, want %q", got, kind)
	}
	if len(fx.clusters) != 1 {
		t.Fatalf("cluster creations = %d, want 1", len(fx.clusters))
	}
}

func TestInstallKindRejectsChecksumMismatchBeforeClusterCreate(t *testing.T) {
	fx := newKindFixture(t, []byte("kind binary"))
	fx.files.files[fx.cfg.KindPath] = []byte("tampered")

	err := installKind(context.Background(), fx.deps, fx.cfg)
	want := fmt.Sprintf("SHA256 mismatch for kind.exe! Expected: %s, Got: %s",
		fx.cfg.SHA256, installSum([]byte("tampered")))
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if len(fx.clusters) != 0 {
		t.Fatalf("cluster created despite checksum mismatch: %+v", fx.clusters)
	}
	if _, ok := fx.files.appends[fx.cfg.GitHubPath]; ok {
		t.Fatal("GITHUB_PATH appended despite checksum mismatch")
	}
}

func TestInstallKindPropagatesClusterCreateError(t *testing.T) {
	boom := errors.New("boom")
	fx := newKindFixture(t, []byte("kind binary"))
	fx.deps.CreateCluster = func(context.Context, string, string, string) error { return boom }

	err := installKind(context.Background(), fx.deps, fx.cfg)
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want wrapped %v", err, boom)
	}
}

func TestNewClusterNameIsRandomHex(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9a-f]{32}$`)
	first, err := newClusterName()
	if err != nil {
		t.Fatal(err)
	}
	if !pattern.MatchString(first) {
		t.Fatalf("cluster name %q is not 32 lowercase hex characters", first)
	}
	second, err := newClusterName()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("cluster name repeated: %q", first)
	}
}

func TestCreateKindClusterPassesArgsAndInheritedEnvironment(t *testing.T) {
	report := filepath.Join(t.TempDir(), "report.json")
	t.Setenv(kindHelperEnv, report)
	t.Setenv(kindHelperExitEnv, "0")
	t.Setenv("KIND_EXPERIMENTAL_PROVIDER", "podman")
	t.Setenv("DOCKER_HOST", testDockerHost)

	if err := createKindCluster(
		context.Background(),
		os.Args[0],
		testClusterName,
		testKindImage,
	); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var got kindHelperReport
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	want := kindHelperReport{
		Args: []string{
			"create",
			"cluster",
			"--name",
			testClusterName,
			"--image",
			testKindImage,
		},
		KindProvider: "podman",
		DockerHost:   testDockerHost,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kind invocation = %+v, want %+v", got, want)
	}
}

func TestCreateKindClusterReturnsExitErrorOnFailure(t *testing.T) {
	t.Setenv(kindHelperEnv, filepath.Join(t.TempDir(), "report.json"))
	t.Setenv(kindHelperExitEnv, "3")

	err := createKindCluster(context.Background(), os.Args[0], testClusterName, testKindImage)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Fatalf("error = %v, want exit code 3", err)
	}
}
