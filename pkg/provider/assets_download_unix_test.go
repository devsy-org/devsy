//go:build linux || darwin || unix

package provider

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
)

func (s *AssetDownloadSuite) TestLocalFIFOIsOptionalWithoutBlocking() {
	const fifoName = "blocked.svg"
	dir := s.T().TempDir()
	manifest := filepath.Join(dir, "provider.yaml")
	s.Require().NoError(os.WriteFile(manifest, widgetManifest(""), 0o600))
	fifo := filepath.Join(dir, fifoName)
	s.Require().NoError(syscall.Mkfifo(fifo, 0o600))
	s.Require().
		NoError(os.WriteFile(filepath.Join(dir, "good.svg"), []byte(testIconSVGDark), 0o600))
	cfg := s.widgetWithIcons(fifoName, "good.svg")
	cfg.Source = ProviderSource{File: manifest}
	root, err := os.OpenRoot(dir)
	s.Require().NoError(err)
	s.T().Cleanup(func() { s.NoError(root.Close()) })

	// This read-only descriptor does not unblock a read-only FIFO open. It
	// ensures the cleanup writer can open without blocking, even if the
	// preparation goroutine has not reached its own open yet.
	reader, err := root.OpenFile(fifoName, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = reader.Close() })

	done := make(chan error, 1)
	go func() {
		done <- PrepareProviderIcons(context.Background(), config.DefaultContext, cfg, nil)
	}()

	blocked := false
	select {
	case err = <-done:
	case <-time.After(2 * time.Second):
		blocked = true
		// Unblock C1's open, then join all preparation work before failing.
		// Holding the writer until completion also covers a delayed reader.
		writer, openErr := root.OpenFile(fifoName, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		s.Require().NoError(openErr)
		err = <-done
		s.NoError(writer.Close())
	}

	s.False(blocked, "optional local FIFO artwork blocked preparation for at least two seconds")
	s.Require().NoError(err)
	light, err := ReadProviderIcon(config.DefaultContext, cfg, "light")
	s.Require().NoError(err)
	s.True(light.Missing, "a FIFO is unavailable artwork")
	dark, err := ReadProviderIcon(config.DefaultContext, cfg, "dark")
	s.Require().NoError(err)
	s.False(dark.Missing, "the regular sibling icon remains usable")
	s.Equal(base64.StdEncoding.EncodeToString([]byte(testIconSVGDark)), dark.DataBase64)
}
