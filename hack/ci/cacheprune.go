package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	pullRefPattern  = regexp.MustCompile(`^refs/pull/(\d+)/(?:merge|head)$`)
	unitKeyPattern  = regexp.MustCompile(`^go-unit-([A-Za-z]+-[A-Za-z0-9]+)-.+-(\d{4}-W\d{2})$`)
	errBadWeek      = errors.New("week must look like 2026-W48")
	isoWeekPattern  = regexp.MustCompile(`^\d{4}-W\d{2}$`)
	unitPartialPart = "go-unit-partial-"
)

const mainRef = "refs/heads/main"

// ghCache holds the Actions cache fields the prune decision reads.
type ghCache struct {
	ID   int64  `json:"id"`
	Key  string `json:"key"`
	Ref  string `json:"ref"`
	Size int64  `json:"size_in_bytes"`
}

type pruneItem struct {
	cache  ghCache
	reason string
}

type pruneOptions struct {
	repo   string
	week   string
	dryRun bool
}

// cachePruneCommand deletes caches no run can restore any more.
type cachePruneCommand struct {
	runner commandRunner
	stdout io.Writer
}

func cachePruneCLI(args []string, stdout, stderr io.Writer) int {
	opts, err := parsePruneArgs(args, stderr, os.Getenv, time.Now)
	if err != nil {
		return 2
	}
	cmd := cachePruneCommand{runner: execRunner{}, stdout: stdout}
	if err := cmd.run(context.Background(), opts); err != nil {
		_, _ = fmt.Fprintf(stdout, "::error::%v\n", err)
		return 1
	}
	return 0
}

func parsePruneArgs(
	args []string,
	output io.Writer,
	getenv func(string) string,
	now func() time.Time,
) (pruneOptions, error) {
	fs := flag.NewFlagSet("cache-prune", flag.ContinueOnError)
	fs.SetOutput(output)
	repo := fs.String("repo", getenv("GITHUB_REPOSITORY"), "owner/name of the repository")
	week := fs.String("week", isoWeek(now()), "current ISO week, as in the go-unit cache keys")
	dryRun := fs.Bool("dry-run", false, "print the caches without deleting them")
	if err := fs.Parse(args); err != nil {
		return pruneOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	if fs.NArg() > 0 || *repo == "" {
		_, _ = fmt.Fprintln(
			output,
			"usage: ci cache-prune [--repo R] [--week YYYY-Www] [--dry-run]",
		)
		return pruneOptions{}, errUnexpectedArgs
	}
	if !isoWeekPattern.MatchString(*week) {
		_, _ = fmt.Fprintln(output, errBadWeek)
		return pruneOptions{}, errBadWeek
	}
	return pruneOptions{repo: *repo, week: *week, dryRun: *dryRun}, nil
}

func isoWeek(t time.Time) string {
	year, week := t.UTC().ISOWeek()
	return fmt.Sprintf("%04d-W%02d", year, week)
}

func (c cachePruneCommand) run(ctx context.Context, opts pruneOptions) error {
	caches, err := c.listCaches(ctx, opts.repo)
	if err != nil {
		return err
	}
	closed, err := c.closedPulls(ctx, opts.repo, pullNumbers(caches))
	if err != nil {
		return err
	}
	items := selectPrunable(caches, closed, opts.week)
	var freed int64
	for _, item := range items {
		if err := c.prune(ctx, opts, item); err != nil {
			return err
		}
		freed += item.cache.Size
	}
	verb := "deleted"
	if opts.dryRun {
		verb = "would delete"
	}
	_, _ = fmt.Fprintf(c.stdout, "%s %d caches, %d MB\n", verb, len(items), freed/1_000_000)
	return nil
}

func (c cachePruneCommand) prune(ctx context.Context, opts pruneOptions, item pruneItem) error {
	_, _ = fmt.Fprintf(c.stdout, "%d %s on %s, %d MB: %s\n",
		item.cache.ID, logSafe(item.cache.Key), logSafe(item.cache.Ref),
		item.cache.Size/1_000_000, item.reason)
	if opts.dryRun {
		return nil
	}
	path := fmt.Sprintf("repos/%s/actions/caches/%d", opts.repo, item.cache.ID)
	return ghAPI(ctx, c.runner, nil, "-X", "DELETE", path)
}

// selectPrunable picks caches of closed pull requests, and main's go-unit
// caches from earlier weeks once the same runner has one for this week.
func selectPrunable(caches []ghCache, closed map[int]bool, week string) []pruneItem {
	current := make(map[string]bool)
	for _, cache := range caches {
		if runner, cacheWeek, ok := unitCacheWeek(cache); ok && cacheWeek == week {
			current[runner] = true
		}
	}
	var items []pruneItem
	for _, cache := range caches {
		if reason := pruneReason(cache, closed, current, week); reason != "" {
			items = append(items, pruneItem{cache, reason})
		}
	}
	return items
}

func pruneReason(cache ghCache, closed map[int]bool, current map[string]bool, week string) string {
	if number, ok := pullNumber(cache.Ref); ok && closed[number] {
		return "pull request " + strconv.Itoa(number) + " is closed"
	}
	runner, cacheWeek, ok := unitCacheWeek(cache)
	if ok && current[runner] && cacheWeek < week {
		return "replaced by the " + week + " cache"
	}
	return ""
}

// unitCacheWeek reads the runner and week from a full go-unit cache on main.
func unitCacheWeek(cache ghCache) (string, string, bool) {
	if cache.Ref != mainRef || strings.HasPrefix(cache.Key, unitPartialPart) {
		return "", "", false
	}
	match := unitKeyPattern.FindStringSubmatch(cache.Key)
	if match == nil {
		return "", "", false
	}
	return match[1], match[2], true
}

func pullNumber(ref string) (int, bool) {
	match := pullRefPattern.FindStringSubmatch(ref)
	if match == nil {
		return 0, false
	}
	number, err := strconv.Atoi(match[1])
	return number, err == nil
}

func pullNumbers(caches []ghCache) []int {
	var numbers []int
	for _, cache := range caches {
		if number, ok := pullNumber(cache.Ref); ok && !slices.Contains(numbers, number) {
			numbers = append(numbers, number)
		}
	}
	slices.Sort(numbers)
	return numbers
}

func (c cachePruneCommand) listCaches(ctx context.Context, repo string) ([]ghCache, error) {
	var out bytes.Buffer
	path := "repos/" + repo + "/actions/caches?per_page=100"
	if err := ghAPI(
		ctx,
		c.runner,
		&out,
		"--paginate",
		path,
		"--jq",
		".actions_caches[]",
	); err != nil {
		return nil, err
	}
	var caches []ghCache
	decoder := json.NewDecoder(&out)
	for {
		var cache ghCache
		err := decoder.Decode(&cache)
		if errors.Is(err, io.EOF) {
			return caches, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode caches: %w", err)
		}
		caches = append(caches, cache)
	}
}

func (c cachePruneCommand) closedPulls(
	ctx context.Context,
	repo string,
	numbers []int,
) (map[int]bool, error) {
	closed := make(map[int]bool)
	for _, number := range numbers {
		var state bytes.Buffer
		path := fmt.Sprintf("repos/%s/pulls/%d", repo, number)
		if err := ghAPI(ctx, c.runner, &state, path, "--jq", ".state"); err != nil {
			return nil, err
		}
		closed[number] = strings.TrimSpace(state.String()) == "closed"
	}
	return closed, nil
}
