package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Command describes one subprocess. Env is an overlay applied to the parent
// environment of that process only; nothing mutates the global environment.
type Command struct {
	Path    string
	Args    []string
	Env     []string // NAME=value overlay; replaces same-named parent entries
	Dir     string
	Timeout time.Duration
	// Stdout and Stderr optionally receive output live, in addition to the
	// tail-capped capture returned in CommandResult.
	Stdout io.Writer
	Stderr io.Writer
}

// CommandResult is the bounded outcome of a Command. Stdout and Stderr hold
// only the last maxCaptureBytes of output.
type CommandResult struct {
	ExitCode int // -1 when the process did not exit normally
	Stdout   string
	Stderr   string
	TimedOut bool
}

// Runner executes a Command. A nonzero exit status is reported through
// CommandResult.ExitCode, not through the error; the error is reserved for
// failure to start or for cancellation of the parent context.
type Runner interface {
	Run(context.Context, Command) (CommandResult, error)
}

// FileStore is the filesystem seam. WriteFile is atomic (temp file + rename).
type FileStore interface {
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm fs.FileMode) error
	AppendFile(name string, data []byte, perm fs.FileMode) error
	MkdirAll(path string, perm fs.FileMode) error
	Stat(name string) (fs.FileInfo, error)
	Remove(name string) error
	RemoveAll(path string) error
}

// Clock is the time seam.
type Clock interface {
	Now() time.Time
	Since(time.Time) time.Duration
	Sleep(context.Context, time.Duration) error
}

// Dialer is the network seam used for readiness probes.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// Downloader fetches an HTTPS resource into dst, failing if it exceeds maxBytes.
type Downloader interface {
	Download(ctx context.Context, rawURL string, dst io.Writer, maxBytes int64) error
}

// Dependencies bundles every side effect so command logic is unit-testable.
type Dependencies struct {
	Run   Runner
	Files FileStore
	Clock Clock
	Net   Dialer
	HTTP  Downloader

	GOOS    string
	Environ func() []string
	Out     io.Writer
	Err     io.Writer
}

func newDefaultDependencies() *Dependencies {
	return &Dependencies{
		Run:     ExecRunner{},
		Files:   osFileStore{},
		Clock:   realClock{},
		Net:     &net.Dialer{},
		HTTP:    httpsDownloader{client: newHTTPSClient()},
		GOOS:    runtime.GOOS,
		Environ: os.Environ,
		Out:     os.Stdout,
		Err:     os.Stderr,
	}
}

type realClock struct{}

func (realClock) Now() time.Time                  { return time.Now() }
func (realClock) Since(t time.Time) time.Duration { return time.Since(t) }

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type osFileStore struct{}

func (osFileStore) ReadFile(name string) ([]byte, error)      { return os.ReadFile(name) }
func (osFileStore) MkdirAll(p string, perm fs.FileMode) error { return os.MkdirAll(p, perm) }
func (osFileStore) Stat(name string) (fs.FileInfo, error)     { return os.Stat(name) }
func (osFileStore) Remove(name string) error                  { return os.Remove(name) }
func (osFileStore) RemoveAll(p string) error                  { return os.RemoveAll(p) }

func (osFileStore) AppendFile(name string, data []byte, perm fs.FileMode) error {
	f, err := os.OpenFile(name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (osFileStore) WriteFile(name string, data []byte, perm fs.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(name), filepath.Base(name)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), name)
}

const (
	maxDownloadRedirects = 5
	downloadTimeout      = 5 * time.Minute
)

func newHTTPSClient() *http.Client {
	return &http.Client{
		Timeout: downloadTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxDownloadRedirects {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to non-HTTPS URL %q refused", req.URL.Redacted())
			}
			return nil
		},
	}
}

type httpsDownloader struct{ client *http.Client }

func (d httpsDownloader) Download(
	ctx context.Context,
	rawURL string,
	dst io.Writer,
	maxBytes int64,
) error {
	u, err := parseHTTPSURL(rawURL)
	if err != nil {
		return err
	}
	resp, err := d.get(ctx, u)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	n, err := io.Copy(dst, io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return fmt.Errorf("download %s: %w", u.Redacted(), err)
	}
	if n > maxBytes {
		return fmt.Errorf("download %s: exceeds %d bytes", u.Redacted(), maxBytes)
	}
	return nil
}

func parseHTTPSURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("download requires an absolute HTTPS URL")
	}
	return u, nil
}

// get returns a 200 response; the caller closes the body.
func (d httpsDownloader) get(ctx context.Context, u *url.URL) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", u.Redacted(), err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("download %s: HTTP %d", u.Redacted(), resp.StatusCode)
	}
	return resp, nil
}
