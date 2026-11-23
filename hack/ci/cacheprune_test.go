package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

const (
	pruneCachesPath = "repos/o/r/actions/caches?per_page=100"
	pruneWeek       = "2026-W49"
	unitLinuxW48    = "go-unit-Linux-X64-1.26.9-abc-2026-W48"
	unitLinuxW49    = "go-unit-Linux-X64-1.26.9-abc-2026-W49"
)

// pruneFakeRunner serves `gh api` output by API path and records every call.
type pruneFakeRunner struct {
	outputs map[string]string
	// failures maps a DELETE path to the stderr gh prints when it fails.
	failures map[string]string
	calls    []string
}

func (f *pruneFakeRunner) Run(_ context.Context, spec commandSpec) error {
	call := strings.Join(spec.Args, " ")
	f.calls = append(f.calls, call)
	if slices.Contains(spec.Args, "DELETE") {
		for path, stderr := range f.failures {
			if slices.Contains(spec.Args, path) {
				_, _ = io.WriteString(spec.Stderr, stderr)
				return errors.New("exit status 1")
			}
		}
		return nil
	}
	for path, out := range f.outputs {
		if slices.Contains(spec.Args, path) {
			_, err := io.WriteString(spec.Stdout, out)
			return err
		}
	}
	return errors.New("unexpected call: " + call)
}

func (f *pruneFakeRunner) deletes() []string {
	var deletes []string
	for _, call := range f.calls {
		if strings.Contains(call, "DELETE") {
			deletes = append(deletes, call)
		}
	}
	return deletes
}

type cachePruneSuite struct {
	suite.Suite
}

func TestCachePruneSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(cachePruneSuite))
}

func cache(id int64, key, ref string) ghCache {
	return ghCache{ID: id, Key: key, Ref: ref, Size: 2_000_000}
}

func prunedIDs(items []pruneItem) []int64 {
	var ids []int64
	for _, item := range items {
		ids = append(ids, item.cache.ID)
	}
	return ids
}

type pruneSelectionCase struct {
	name   string
	caches []ghCache
	want   []int64
}

func pruneSelectionCases() []pruneSelectionCase {
	return []pruneSelectionCase{
		{
			name: "closed pull request caches go, open ones stay",
			caches: []ghCache{
				cache(1, "mise-v1-linux", "refs/pull/1896/merge"),
				cache(2, "setup-go-Linux", "refs/pull/1896/head"),
				cache(3, "mise-v1-linux", "refs/pull/1891/merge"),
				cache(4, "mise-v1-linux", "refs/pull/1777/merge"),
			},
			want: []int64{1, 2},
		},
		{
			name: "older week goes once the same restore group has this week",
			caches: []ghCache{
				cache(1, unitLinuxW48, mainRef),
				cache(2, unitLinuxW49, mainRef),
				cache(3, "go-unit-Linux-X64-1.26.9-def-2026-W47", mainRef),
			},
			want: []int64{1, 3},
		},
		{
			name: "older week stays when this week's cache is for another Go or cache version",
			caches: []ghCache{
				cache(1, "go-unit-Linux-X64-1.26.9-abc-2026-W48", mainRef),
				cache(2, "go-unit-Linux-X64-1.26.8-abc-2026-W49", mainRef),
				cache(3, "go-unit-macOS-ARM64-1.26.9-abc-2026-W48", mainRef),
				{
					ID:      4,
					Key:     "***************************************",
					Ref:     mainRef,
					Version: "v2",
				},
			},
		},
		{
			name: "older week stays until this week's cache exists",
			caches: []ghCache{
				cache(1, unitLinuxW48, mainRef),
				cache(2, "go-unit-macOS-ARM64-1.26.9-abc-2026-W49", mainRef),
				cache(3, "go-unit-Windows-X64-1.26.9-def-2026-W48", mainRef),
			},
		},
		{
			name: "partial, newer-week and branch caches stay",
			caches: []ghCache{
				cache(1, unitLinuxW49, mainRef),
				cache(2, "go-unit-partial-Linux-X64-1.26.9-45261731628", mainRef),
				cache(3, "go-unit-Linux-X64-1.26.9-abc-2026-W50", mainRef),
				cache(4, unitLinuxW48, "refs/heads/release"),
				cache(5, "setup-go-Linux-x64-ubuntu26-go-1.26.9-abc", mainRef),
			},
		},
	}
}

func (s *cachePruneSuite) TestSelectPrunable() {
	closed := map[int]bool{1896: true, 1891: false}
	for _, tt := range pruneSelectionCases() {
		s.Run(tt.name, func() {
			s.Equal(tt.want, prunedIDs(selectPrunable(tt.caches, closed, pruneWeek)))
		})
	}
}

func (s *cachePruneSuite) TestDeletesSelectedCaches() {
	fake := s.fixture()
	var out bytes.Buffer

	err := cachePruneCommand{runner: fake, stdout: &out}.run(
		context.Background(), pruneOptions{repo: testRepo, week: pruneWeek},
	)

	s.Require().NoError(err)
	s.Equal([]string{
		"api -X DELETE repos/o/r/actions/caches/7",
		"api -X DELETE repos/o/r/actions/caches/9",
	}, fake.deletes())
	s.Contains(
		out.String(),
		`7 "`+unitLinuxW48+`" on "refs/heads/main", 2000 MB: replaced by the 2026-W49 cache`,
	)
	s.Contains(
		out.String(),
		`9 "mise-v1" on "refs/pull/12/merge", 208 MB: pull request 12 is closed`,
	)
	s.Contains(out.String(), "deleted 2 caches, 2208 MB\n")
}

func (s *cachePruneSuite) TestDryRunDeletesNothing() {
	fake := s.fixture()
	var out bytes.Buffer

	err := cachePruneCommand{runner: fake, stdout: &out}.run(
		context.Background(), pruneOptions{repo: testRepo, week: pruneWeek, dryRun: true},
	)

	s.Require().NoError(err)
	s.Empty(fake.deletes())
	s.Contains(out.String(), "would delete 2 caches, 2208 MB\n")
}

func (s *cachePruneSuite) TestKeysCannotStartWorkflowCommands() {
	fake := &pruneFakeRunner{outputs: map[string]string{
		pruneCachesPath:      `{"id":9,"key":"k\n::error::x ##[error]y","ref":"refs/pull/12/merge","size_in_bytes":1}`,
		"repos/o/r/pulls/12": "closed",
	}}
	var out bytes.Buffer

	err := cachePruneCommand{runner: fake, stdout: &out}.run(
		context.Background(), pruneOptions{repo: testRepo, week: pruneWeek, dryRun: true},
	)

	s.Require().NoError(err)
	s.Contains(out.String(), `"k\n::error::x \x23\x23[error]y"`)
	s.NotContains(out.String(), "##[")
	s.NotContains(out.String(), "\n::")
}

func (s *cachePruneSuite) TestDeleteErrors() {
	tests := []struct {
		name    string
		stderr  string
		wantErr string
		wantOut string
	}{
		{
			name: "evicted after listing", stderr: "gh: Not Found (HTTP 404)\n",
			wantOut: "7 was already gone\n",
		},
		{
			name: "server error", stderr: "gh: Server Error (HTTP 500)\n",
			wantErr: "(HTTP 500)",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			fake := s.fixture()
			fake.failures = map[string]string{"repos/o/r/actions/caches/7": tt.stderr}
			var out bytes.Buffer

			err := cachePruneCommand{runner: fake, stdout: &out}.run(
				context.Background(), pruneOptions{repo: testRepo, week: pruneWeek},
			)

			if tt.wantErr != "" {
				s.Require().ErrorContains(err, tt.wantErr)
				s.Len(fake.deletes(), 1)
				return
			}
			s.Require().NoError(err)
			s.Contains(out.String(), tt.wantOut)
			s.Len(fake.deletes(), 2)
			s.Contains(out.String(), "deleted 1 caches, 208 MB\n")
		})
	}
}

func (s *cachePruneSuite) TestPullLookupErrorFails() {
	fake := &pruneFakeRunner{outputs: map[string]string{
		pruneCachesPath: `{"id":9,"key":"k","ref":"refs/pull/12/merge","size_in_bytes":1}`,
	}}

	err := cachePruneCommand{runner: fake, stdout: io.Discard}.run(
		context.Background(), pruneOptions{repo: testRepo, week: pruneWeek},
	)

	s.Require().ErrorContains(err, "unexpected call")
	s.Empty(fake.deletes())
}

func (s *cachePruneSuite) TestArgs() {
	getenv := func(string) string { return testRepo }
	now := func() time.Time { return time.Date(2026, 11, 30, 1, 0, 0, 0, time.UTC) }
	var output bytes.Buffer

	opts, err := parsePruneArgs([]string{"--dry-run"}, &output, getenv, now)

	s.Require().NoError(err)
	s.Equal(pruneOptions{repo: testRepo, week: pruneWeek, dryRun: true}, opts)
	for _, args := range [][]string{{"stray"}, {"--week", "2026-48"}} {
		_, err := parsePruneArgs(args, &output, getenv, now)
		s.Require().Error(err)
	}
	_, err = parsePruneArgs(nil, &output, func(string) string { return "" }, now)
	s.Require().Error(err)
}

func (s *cachePruneSuite) TestISOWeekAtYearEnd() {
	s.Equal("2026-W53", isoWeek(time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)))
	s.Equal("2027-W01", isoWeek(time.Date(2027, 1, 3, 23, 0, 0, 0, time.FixedZone("x", -3*3600))))
}

func (s *cachePruneSuite) fixture() *pruneFakeRunner {
	return &pruneFakeRunner{outputs: map[string]string{
		pruneCachesPath: `{"id":7,"key":"` + unitLinuxW48 + `","ref":"refs/heads/main","size_in_bytes":2000000000}
{"id":8,"key":"` + unitLinuxW49 + `","ref":"refs/heads/main","size_in_bytes":2000000000}
{"id":9,"key":"mise-v1","ref":"refs/pull/12/merge","size_in_bytes":208000000}
{"id":10,"key":"mise-v1","ref":"refs/pull/13/merge","size_in_bytes":208000000}
`,
		"repos/o/r/pulls/12": "closed\n",
		"repos/o/r/pulls/13": "open\n",
	}}
}
