package devcontainer

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/compose"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/feature"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

const (
	testBuildPhase = "build"
	testArch       = "amd64"
)

var testComposeArgs = []string{"build"}

func recordedContext(t *testing.T) (context.Context, *workspacelogs.Store, string) {
	t.Helper()
	store, err := workspacelogs.Open(workspacelogs.Options{Root: t.TempDir()})
	require.NoError(t, err)
	m, err := store.CreateRun(workspacelogs.RunSpec{
		Context: recordTestContext, WorkspaceUID: "uid", Command: "up",
	})
	require.NoError(t, err)
	rec, err := workspacelogs.NewRecorder(workspacelogs.RecorderOptions{
		Backend: store, RunID: m.RunID, Location: workspacelogs.LocationProviderHost,
	})
	require.NoError(t, err)

	return workspacelogs.WithRecorder(t.Context(), rec), store, m.RunID
}

func readRecords(t *testing.T, store *workspacelogs.Store, runID string) []workspacelogs.Record {
	t.Helper()
	var all []workspacelogs.Record
	req := workspacelogs.ReadRequest{RunID: runID}
	for {
		resp, err := store.Read(req)
		require.NoError(t, err)
		all = append(all, resp.Records...)
		if !resp.HasMore {
			return all
		}
		req.After = resp.NextCursor
	}
}

func buildInvocations(recs []workspacelogs.Record) []workspacelogs.Record {
	var out []workspacelogs.Record
	for i := range recs {
		if recs[i].Kind == workspacelogs.KindInvocation {
			out = append(out, recs[i])
		}
	}

	return out
}

func combinedText(recs []workspacelogs.Record) string {
	var b strings.Builder
	for i := range recs {
		if recs[i].Kind == workspacelogs.KindOutput &&
			recs[i].Stream == workspacelogs.StreamCombined {
			b.WriteString(recs[i].Text)
		}
	}

	return b.String()
}

func composeScript(t *testing.T, body string) *compose.ComposeHelper {
	t.Helper()
	path := filepath.Join(t.TempDir(), "compose-fake")
	//nolint:gosec // test helper script needs exec bit
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755))

	return &compose.ComposeHelper{Command: path}
}

func TestRunComposeBuildRetainsOutputAtWarnLevel(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.WarnLevel)
	ctx, store, runID := recordedContext(t)
	helper := composeScript(t, "echo 'compose out'; echo 'compose err' >&2")

	err := (&runner{}).runComposeBuild(ctx, helper, testComposeArgs)

	require.NoError(t, err)
	assert.Empty(t, logs.All())
	recs := readRecords(t, store, runID)
	// Compose pipes stdout and stderr separately, so the order is not fixed.
	assert.Contains(t, combinedText(recs), "compose out\n")
	assert.Contains(t, combinedText(recs), "compose err\n")
	inv := buildInvocations(recs)
	require.Len(t, inv, 2)
	assert.Equal(t, "compose", inv[0].HookKey)
	assert.Equal(t, testBuildPhase, inv[0].Phase)
	assert.Empty(t, inv[0].Hook)
	assert.Equal(t, workspacelogs.InvocationSucceeded, inv[1].State)
}

func TestRunComposeBuildFailureKeepsErrorAndExitCode(t *testing.T) {
	log.InitTestObserved(t, zapcore.WarnLevel)
	ctx, store, runID := recordedContext(t)
	helper := composeScript(t, "echo 'service failed' >&2; exit 4")

	err := (&runner{}).runComposeBuild(ctx, helper, testComposeArgs)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "service failed")
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	recs := readRecords(t, store, runID)
	assert.Equal(t, "service failed\n", combinedText(recs))
	inv := buildInvocations(recs)
	require.Len(t, inv, 2)
	assert.Equal(t, workspacelogs.InvocationFailed, inv[1].State)
	require.NotNil(t, inv[1].ExitCode)
	assert.Equal(t, 4, *inv[1].ExitCode)
}

func TestRunComposeBuildCanceledIsRecorded(t *testing.T) {
	log.InitTestObserved(t, zapcore.WarnLevel)
	ctx, store, runID := recordedContext(t)
	ctx, cancel := context.WithCancel(ctx)
	cancel()

	err := (&runner{}).runComposeBuild(ctx, composeScript(t, "sleep 5"), testComposeArgs)

	require.Error(t, err)
	inv := buildInvocations(readRecords(t, store, runID))
	require.Len(t, inv, 2)
	assert.Equal(t, workspacelogs.InvocationCanceled, inv[1].State)
}

func TestRunComposeBuildWithoutRecorderLogsOnly(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.InfoLevel)

	err := (&runner{}).runComposeBuild(
		t.Context(), composeScript(t, "echo plain"), testComposeArgs,
	)

	require.NoError(t, err)
	require.Len(t, logs.All(), 1)
	assert.Equal(t, "plain", logs.All()[0].Message)
}

func TestBuildImageRecordsPrebuildReuseWithoutOutput(t *testing.T) {
	log.InitTestObserved(t, zapcore.WarnLevel)
	ctx, store, runID := recordedContext(t)
	server := httptest.NewServer(registry.New())
	t.Cleanup(server.Close)
	repo := strings.TrimPrefix(server.URL, "http://") + "/prebuilds"
	contextDir := t.TempDir()
	parsed := &config.SubstitutedConfig{Config: &config.DevContainerConfig{}}
	parsed.Config.Origin = filepath.Join(contextDir, "devcontainer.json")
	hash, err := config.CalculatePrebuildHash(config.PrebuildHashParams{
		Config:       parsed.Config,
		Architecture: testArch,
		ContextPath:  config.GetContextPath(parsed.Config),
	})
	require.NoError(t, err)
	img, err := random.Image(64, 1)
	require.NoError(t, err)
	ref, err := name.ParseReference(repo + ":" + hash)
	require.NoError(t, err)
	require.NoError(t, remote.Write(ref, img))
	r := newTestRunner(&mockDriver{})
	r.imageBackend = &separateImages{
		details:   &config.ImageDetails{ID: "prebuilt"},
		buildInfo: &config.BuildInfo{ImageName: "must-not-build"},
	}

	info, err := r.buildImage(ctx, &buildImageParams{
		parsedConfig:      parsed,
		extendedBuildInfo: &feature.ExtendedBuildInfo{},
		options: provider.BuildOptions{
			CLIOptions: provider.CLIOptions{PrebuildRepositories: []string{repo}},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, repo+":"+hash, info.ImageName)
	recs := readRecords(t, store, runID)
	inv := buildInvocations(recs)
	require.Len(t, inv, 1)
	assert.Equal(t, workspacelogs.InvocationSkipped, inv[0].State)
	assert.Equal(t, "prebuild_reused", inv[0].Reason)
	assert.Equal(t, testBuildPhase, inv[0].Phase)
	for i := range recs {
		assert.NotEqual(t, workspacelogs.KindOutput, recs[i].Kind, "no build ran, so no output")
	}
}
