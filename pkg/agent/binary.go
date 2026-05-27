package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
	devsyhttp "github.com/devsy-org/devsy/pkg/http"
	"github.com/devsy-org/devsy/pkg/log"
)

type BinarySource interface {
	GetBinary(ctx context.Context, arch string) (io.ReadCloser, error)
	SourceName() string
}

type BinaryManager struct {
	sources []BinarySource
}

func NewBinaryManager(downloadURL string) (*BinaryManager, error) {
	cachePath, err := config.DefaultPathManager().AgentCacheDir()
	if err != nil {
		return nil, fmt.Errorf("agent cache dir: %w", err)
	}

	cache := &BinaryCache{BaseDir: cachePath}

	expectedVersion := versionFromDownloadURL(downloadURL)

	return &BinaryManager{
		sources: []BinarySource{
			&InjectSource{},
			&FileCacheSource{Cache: cache, ExpectedVersion: expectedVersion},
			&HTTPDownloadSource{BaseURL: downloadURL, Cache: cache, Version: expectedVersion},
		},
	}, nil
}

func versionFromDownloadURL(downloadURL string) string {
	parts := strings.Split(strings.TrimRight(downloadURL, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	last := parts[len(parts)-1]
	if strings.HasPrefix(last, "v") {
		return last
	}
	return ""
}

func (m *BinaryManager) AcquireBinary(ctx context.Context, arch string) (io.ReadCloser, error) {
	for _, source := range m.sources {
		binary, err := source.GetBinary(ctx, arch)
		if err == nil {
			log.Debugf("acquired binary from %s", source.SourceName())
			return binary, nil
		}
		log.Debugf("source %s failed: %v", source.SourceName(), err)
	}
	return nil, ErrBinaryNotFound
}

type BinaryCache struct {
	BaseDir string
}

func (c *BinaryCache) Get(arch string) (io.ReadCloser, error) {
	return os.Open(c.pathFor(arch))
}

func (c *BinaryCache) Set(arch string, data io.Reader) error {
	return c.atomicWrite(c.pathFor(arch), data)
}

func (c *BinaryCache) WriteVersion(arch, ver string) {
	_ = os.WriteFile(c.versionPathFor(arch), []byte(ver), 0o600) // #nosec G306
}

func (c *BinaryCache) ReadVersion(arch string) string {
	data, err := os.ReadFile(c.versionPathFor(arch))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (c *BinaryCache) pathFor(arch string) string {
	return filepath.Join(c.BaseDir, config.BinaryName+"-"+osLinux+"-"+arch)
}

func (c *BinaryCache) versionPathFor(arch string) string {
	return c.pathFor(arch) + ".version"
}

func (c *BinaryCache) atomicWrite(path string, data io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil { // #nosec G301
		return err
	}

	file, err := os.CreateTemp(filepath.Dir(path), config.BinaryName+"-*.tmp")
	if err != nil {
		return err
	}
	temp := file.Name()

	if _, err := io.Copy(file, data); err != nil {
		_ = file.Close()
		_ = os.Remove(temp)
		return err
	}

	if err := file.Chmod(0o755); err != nil {
		_ = file.Close()
		_ = os.Remove(temp)
		return err
	}

	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return err
	}

	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

type InjectSource struct{}

func (s *InjectSource) GetBinary(ctx context.Context, arch string) (io.ReadCloser, error) {
	if runtime.GOOS != osLinux {
		return nil, fmt.Errorf(
			"%w: host OS %q cannot supply a linux binary for the container",
			ErrArchMismatch,
			runtime.GOOS,
		)
	}
	if runtime.GOARCH != arch {
		return nil, fmt.Errorf(
			"%w: host GOARCH %q does not match container arch %q",
			ErrArchMismatch,
			runtime.GOARCH,
			arch,
		)
	}
	return s.openCurrentExecutable()
}

func (s *InjectSource) SourceName() string {
	return "local executable"
}

func (s *InjectSource) openCurrentExecutable() (io.ReadCloser, error) {
	path, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return os.Open(path) // #nosec G304
}

type FileCacheSource struct {
	Cache           *BinaryCache
	ExpectedVersion string
}

func (s *FileCacheSource) GetBinary(ctx context.Context, arch string) (io.ReadCloser, error) {
	if s.ExpectedVersion != "" {
		cached := s.Cache.ReadVersion(arch)
		if cached != s.ExpectedVersion {
			return nil, fmt.Errorf(
				"cache version %q does not match expected %q",
				cached, s.ExpectedVersion,
			)
		}
	}
	return s.Cache.Get(arch)
}

func (s *FileCacheSource) SourceName() string {
	return "local cache"
}

type HTTPDownloadSource struct {
	BaseURL string
	Cache   *BinaryCache
	Version string
}

func (s *HTTPDownloadSource) GetBinary(ctx context.Context, arch string) (io.ReadCloser, error) {
	downloadURL, err := s.buildDownloadURL(arch)
	if err != nil {
		return nil, err
	}

	resp, err := s.downloadFile(ctx, downloadURL)
	if err != nil {
		return nil, err
	}

	if s.Cache != nil {
		return s.cacheAndReturn(arch, resp.Body)
	}

	return resp.Body, nil
}

func (s *HTTPDownloadSource) SourceName() string {
	return "http download"
}

func (s *HTTPDownloadSource) buildDownloadURL(arch string) (string, error) {
	binaryName := config.BinaryName + "-" + osLinux + "-" + arch
	downloadURL, err := url.JoinPath(s.BaseURL, binaryName)
	if err != nil {
		return "", fmt.Errorf("failed to construct download URL: %w", err)
	}
	return downloadURL, nil
}

func (s *HTTPDownloadSource) downloadFile(
	ctx context.Context,
	downloadURL string,
) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := devsyhttp.GetHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download binary: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("bad status: %s", resp.Status)
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/html") {
		_ = resp.Body.Close()
		return nil, fmt.Errorf(
			"received HTML instead of binary from %s (check if the download URL is correct)",
			downloadURL,
		)
	}

	return resp, nil
}

// cacheAndReturn streams the binary to the caller while simultaneously caching it.
// The caller MUST fully read or close the returned reader to avoid goroutine leaks.
func (s *HTTPDownloadSource) cacheAndReturn(
	arch string,
	body io.ReadCloser,
) (io.ReadCloser, error) {
	pr, pw := io.Pipe()

	go func() {
		var streamErr error
		defer func() {
			_ = body.Close()
			if streamErr != nil {
				_ = pw.CloseWithError(streamErr)
			} else {
				_ = pw.Close()
			}
		}()

		if !s.prepareCacheDir(arch, body, pw, &streamErr) {
			return
		}

		s.streamAndCache(arch, body, pw, &streamErr)
	}()

	return pr, nil
}

func (s *HTTPDownloadSource) prepareCacheDir(
	arch string,
	body io.ReadCloser,
	pw *io.PipeWriter,
	streamErr *error,
) bool {
	cachePath := s.Cache.pathFor(arch)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o750); err != nil { // #nosec G301
		// Cache directory creation failed; fall back to direct streaming
		if _, copyErr := io.Copy(pw, body); copyErr != nil {
			*streamErr = fmt.Errorf("mkdir failed (%v), fallback copy failed: %w", err, copyErr)
		}
		return false
	}
	return true
}

func (s *HTTPDownloadSource) streamAndCache(
	arch string,
	body io.ReadCloser,
	pw *io.PipeWriter,
	streamErr *error,
) {
	cachePath := s.Cache.pathFor(arch)
	file, tmpPath, err := s.createTempFile(cachePath, body, pw, streamErr)
	if err != nil {
		return
	}

	success := false
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	if !s.writeToFile(file, body, pw, streamErr) {
		return
	}

	closeErr := file.Close()
	closed = true
	if closeErr != nil {
		*streamErr = closeErr
		return
	}

	if err := os.Rename(tmpPath, cachePath); err == nil {
		success = true
		if s.Version != "" && s.Cache != nil {
			s.Cache.WriteVersion(arch, s.Version)
		}
	}
}

func (s *HTTPDownloadSource) createTempFile(
	cachePath string,
	body io.ReadCloser,
	pw *io.PipeWriter,
	streamErr *error,
) (*os.File, string, error) {
	file, err := os.CreateTemp(filepath.Dir(cachePath), config.BinaryName+"-agent-*.tmp")
	if err != nil {
		if _, copyErr := io.Copy(pw, body); copyErr != nil {
			*streamErr = copyErr
		}
		return nil, "", err
	}
	return file, file.Name(), nil
}

func (s *HTTPDownloadSource) writeToFile(
	file *os.File,
	body io.ReadCloser,
	pw *io.PipeWriter,
	streamErr *error,
) bool {
	mw := io.MultiWriter(file, pw)
	if _, err := io.Copy(mw, body); err != nil {
		*streamErr = err
		return false
	}

	if err := file.Chmod(0o755); err != nil {
		*streamErr = err
		return false
	}

	if err := file.Sync(); err != nil {
		*streamErr = err
		return false
	}

	return true
}
