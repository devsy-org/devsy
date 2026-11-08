package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/hash"
	"github.com/devsy-org/devsy/pkg/provider"
)

const (
	prepareProviderCommand = "prepare-provider"
	providerManifestName   = "provider.yaml"
	providerCacheDirName   = "cache"
	runtimeDriverBinary    = "DEVSY_RUNTIME_DRIVER"
	defaultProviderRepo    = "devsy-org/devsy-provider-microsandbox"
	defaultGithubBaseURL   = "https://github.com"
	providerFetchAttempts  = 6
	providerFetchBaseDelay = 5 * time.Second
	providerFetchMaxDelay  = time.Minute
	providerFetchTimeout   = 5 * time.Minute
)

type statusError struct {
	code int
	url  string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("received status code %d when trying to download %s", e.code, e.url)
}

type localError struct{ err error }

func (e *localError) Error() string { return e.err.Error() }

func (e *localError) Unwrap() error { return e.err }

type providerAssets struct {
	baseURL    string
	repository string
	version    string
	outputDir  string
	goos       string
	goarch     string
	attempts   int
	baseDelay  time.Duration
	client     *http.Client
	sleep      func(context.Context, time.Duration) error
}

func prepareProviderCLI(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(prepareProviderCommand, flag.ContinueOnError)
	flags.SetOutput(stderr)
	assets := providerAssets{
		baseURL:   defaultGithubBaseURL,
		attempts:  providerFetchAttempts,
		baseDelay: providerFetchBaseDelay,
		client:    &http.Client{Timeout: providerFetchTimeout},
		sleep:     sleepContext,
	}
	flags.StringVar(&assets.repository, "repository", defaultProviderRepo, "provider repository")
	flags.StringVar(&assets.version, "version", "", "pinned provider release tag")
	flags.StringVar(&assets.outputDir, "output-dir", "", "directory receiving the assets")
	flags.StringVar(&assets.goos, "os", runtime.GOOS, "runtime binary operating system")
	flags.StringVar(&assets.goarch, "arch", runtime.GOARCH, "runtime binary architecture")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || assets.version == "" || assets.outputDir == "" {
		_, _ = fmt.Fprintln(
			stderr,
			"prepare-provider requires --version and --output-dir and takes no arguments",
		)
		return 2
	}
	if err := assets.prepare(context.Background(), stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "::error::%v\n", err)
		return 1
	}
	return 0
}

func (a *providerAssets) prepare(ctx context.Context, out io.Writer) error {
	manifestPath := filepath.Join(a.outputDir, providerManifestName)
	config, err := a.loadManifest(ctx, manifestPath)
	if err != nil {
		return err
	}
	binary, err := a.runtimeBinary(config)
	if err != nil {
		return err
	}
	target := filepath.Join(a.outputDir, providerCacheDirName, hash.String(binary.Path))
	if verifyFileSHA256(target, binary.Checksum) == nil {
		_, _ = fmt.Fprintf(out, "provider %s runtime binary already prepared\n", a.version)
		return nil
	}
	if err := a.fetch(ctx, binary.Path, target); err != nil {
		return fmt.Errorf("download runtime binary: %w", err)
	}
	if err := verifyFileSHA256(target, binary.Checksum); err != nil {
		return errors.Join(err, removeIfPresent(target))
	}
	_, _ = fmt.Fprintf(
		out,
		"prepared provider %s runtime binary %s/%s\n",
		a.version,
		a.goos,
		a.goarch,
	)
	return nil
}

func (a *providerAssets) loadManifest(
	ctx context.Context,
	path string,
) (*provider.ProviderConfig, error) {
	config, err := parseManifestFile(path)
	if err == nil && config.Version == a.version && a.boundToRelease(config) {
		return config, nil
	}
	manifestURL := fmt.Sprintf(
		"%s/%s/releases/download/%s/%s",
		a.baseURL, a.repository, url.PathEscape(a.version), providerManifestName,
	)
	if err := a.fetch(ctx, manifestURL, path); err != nil {
		return nil, fmt.Errorf("download provider manifest: %w", err)
	}
	config, err = parseManifestFile(path)
	if err != nil {
		return nil, errors.Join(err, removeIfPresent(path))
	}
	if config.Version != a.version || !a.boundToRelease(config) {
		mismatch := fmt.Errorf(
			"provider manifest version %q does not match release %s%s",
			config.Version, a.releasePrefix(), providerManifestName,
		)
		return nil, errors.Join(mismatch, removeIfPresent(path))
	}
	return config, nil
}

func (a *providerAssets) releasePrefix() string {
	return fmt.Sprintf(
		"%s/%s/releases/download/%s/",
		a.baseURL,
		a.repository,
		url.PathEscape(a.version),
	)
}

func (a *providerAssets) boundToRelease(config *provider.ProviderConfig) bool {
	binary, err := a.runtimeBinary(config)
	return err == nil && strings.HasPrefix(binary.Path, a.releasePrefix())
}

func parseManifestFile(path string) (_ *provider.ProviderConfig, err error) {
	file, err := os.Open(path) // #nosec G304 -- path is the helper's own output directory
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	return provider.ParseProvider(file)
}

func (a *providerAssets) runtimeBinary(
	config *provider.ProviderConfig,
) (*provider.ProviderBinary, error) {
	for _, binary := range config.Agent.Binaries[runtimeDriverBinary] {
		if binary.OS != a.goos || binary.Arch != a.goarch {
			continue
		}
		if binary.Checksum == "" || !strings.HasPrefix(binary.Path, "https://") {
			return nil, fmt.Errorf(
				"provider manifest binary for %s/%s needs an https path and a checksum",
				a.goos, a.goarch,
			)
		}
		return binary, nil
	}
	return nil, fmt.Errorf(
		"provider manifest has no %s binary for %s/%s",
		runtimeDriverBinary, a.goos, a.goarch,
	)
}

func (a *providerAssets) fetch(ctx context.Context, rawURL, target string) error {
	var err error
	for attempt := 1; ; attempt++ {
		if err = a.fetchOnce(ctx, rawURL, target); err == nil {
			return nil
		}
		if attempt >= a.attempts || !retryableFetch(ctx, err) {
			return fmt.Errorf("after %d attempts: %w", attempt, err)
		}
		if err := a.sleep(ctx, a.backoff(attempt)); err != nil {
			return err
		}
	}
}

func (a *providerAssets) backoff(attempt int) time.Duration {
	delay := a.baseDelay << (attempt - 1)
	if delay <= 0 || delay > providerFetchMaxDelay {
		return providerFetchMaxDelay
	}
	return delay
}

func retryableFetch(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if _, ok := errors.AsType[*localError](err); ok {
		return false
	}
	if status, ok := errors.AsType[*statusError](err); ok {
		return status.code >= http.StatusInternalServerError
	}
	return true
}

func (a *providerAssets) fetchOnce(ctx context.Context, rawURL, target string) (err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, resp.Body.Close()) }()
	if resp.StatusCode != http.StatusOK {
		return &statusError{code: resp.StatusCode, url: rawURL}
	}
	return saveBody(resp.Body, target)
}

func saveBody(body io.Reader, target string) (err error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return &localError{err}
	}
	partial, err := os.CreateTemp(filepath.Dir(target), ".download-*")
	if err != nil {
		return &localError{err}
	}
	defer func() { err = errors.Join(err, removeIfPresent(partial.Name())) }()
	if _, err := io.Copy(partial, body); err != nil {
		return errors.Join(err, partial.Close())
	}
	if err := partial.Close(); err != nil {
		return &localError{err}
	}
	if err := os.Rename(partial.Name(), target); err != nil {
		return &localError{err}
	}
	return nil
}

func verifyFileSHA256(path, want string) (err error) {
	file, err := os.Open(path) // #nosec G304 -- path is the helper's own output directory
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return err
	}
	if got := hex.EncodeToString(digest.Sum(nil)); !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", path, got, want)
	}
	return nil
}

func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
