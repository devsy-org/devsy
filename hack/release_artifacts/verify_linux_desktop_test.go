package main

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

const goodEntry = "[Desktop Entry]\nName=Devsy\nIcon=devsy-desktop\nStartupWMClass=sh.devsy.app\n"

type VerifyLinuxDesktopSuite struct {
	suite.Suite
	root string
}

func TestVerifyLinuxDesktopSuite(t *testing.T) {
	suite.Run(t, new(VerifyLinuxDesktopSuite))
}

func (s *VerifyLinuxDesktopSuite) SetupTest() {
	s.root = s.T().TempDir()
}

func (s *VerifyLinuxDesktopSuite) TestAcceptsBuilderIconNameAndRuntimeMaster() {
	entry := s.write("applications/sh.devsy.app.desktop", []byte(goodEntry))
	s.icon("hicolor/32x32/apps/devsy-desktop.png", 32)
	s.icon("hicolor/128x128/apps/devsy-desktop.png", 128)
	runtime := s.icon("resources/app-icon.png", 1024)
	s.Require().NoError(s.verify(entry, []string{runtime}))
}

func (s *VerifyLinuxDesktopSuite) TestAcceptsFlatpakExportName() {
	entry := s.write("applications/sh.devsy.app.desktop", []byte(
		"[Desktop Entry]\nIcon=sh.devsy.app\nStartupWMClass=sh.devsy.app\n"))
	s.icon("hicolor/128x128/apps/sh.devsy.app.png", 128)
	s.Require().NoError(s.verify(entry, nil))
}

func (s *VerifyLinuxDesktopSuite) TestRejectsWrongFilenameOrClass() {
	entry := s.write("applications/devsy.desktop", []byte(goodEntry))
	s.Require().ErrorContains(s.verify(entry, nil), "expected sh.devsy.app.desktop")
	entry = s.write("applications/sh.devsy.app.desktop", []byte(
		"[Desktop Entry]\nIcon=devsy-desktop\nStartupWMClass=Devsy\n"))
	s.Require().ErrorContains(s.verify(entry, nil), "StartupWMClass")
}

func (s *VerifyLinuxDesktopSuite) TestRejectsIconOutsideThemePath() {
	entry := s.write("applications/sh.devsy.app.desktop", []byte(goodEntry))
	s.icon("hicolor/wrong/place/devsy-desktop.png", 128)
	s.Require().ErrorContains(s.verify(entry, nil), "missing 128x128 theme icon")
}

func (s *VerifyLinuxDesktopSuite) TestRejectsUndecodableImage() {
	entry := s.write("applications/sh.devsy.app.desktop", []byte(goodEntry))
	s.write("hicolor/128x128/apps/devsy-desktop.png", []byte("png"))
	s.Require().ErrorContains(s.verify(entry, nil), "invalid PNG")
}

func (s *VerifyLinuxDesktopSuite) TestRejectsTruncatedImageData() {
	entry := s.write("applications/sh.devsy.app.desktop", []byte(goodEntry))
	path := s.icon("hicolor/128x128/apps/devsy-desktop.png", 128)
	data, err := os.ReadFile(path)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(path, data[:len(data)-16], 0o600))
	s.Require().ErrorContains(s.verify(entry, nil), "malformed PNG data")
}

func (s *VerifyLinuxDesktopSuite) TestRejectsWrongDimensionsAndExportSize() {
	entry := s.write("applications/sh.devsy.app.desktop", []byte(goodEntry))
	s.icon("hicolor/128x128/apps/devsy-desktop.png", 32)
	s.Require().ErrorContains(s.verify(entry, nil), "PNG dimensions")
	s.icon("hicolor/128x128/apps/devsy-desktop.png", 128)
	s.icon("hicolor/1024x1024/apps/devsy-desktop.png", 1024)
	s.Require().ErrorContains(s.verify(entry, nil), "invalid hicolor size")
}

func (s *VerifyLinuxDesktopSuite) TestRequiresDecodedRuntimePNG() {
	entry := s.write("applications/sh.devsy.app.desktop", []byte(goodEntry))
	s.icon("hicolor/128x128/apps/devsy-desktop.png", 128)
	runtime := s.write("resources/app-icon.png", []byte("not an image"))
	s.Require().ErrorContains(s.verify(entry, []string{runtime}), "invalid PNG")
	s.Require().Error(s.verify(entry, []string{filepath.Join(s.root, "absent")}))
}

func (s *VerifyLinuxDesktopSuite) TestIgnoresKeysOutsideDesktopEntryGroup() {
	entry := s.write("applications/sh.devsy.app.desktop", []byte(
		"[Desktop Entry]\nIcon=devsy-desktop\n[Desktop Action x]\nStartupWMClass=sh.devsy.app\n"))
	s.Require().ErrorContains(s.verify(entry, nil), "StartupWMClass")
}

func (s *VerifyLinuxDesktopSuite) verify(entry string, required []string) error {
	return verifyLinuxDesktop("sh.devsy.app", entry, filepath.Join(s.root, "hicolor"), required)
}

func (s *VerifyLinuxDesktopSuite) write(rel string, content []byte) string {
	path := filepath.Join(s.root, rel)
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o750))
	s.Require().NoError(os.WriteFile(path, content, 0o600))
	return path
}

func (s *VerifyLinuxDesktopSuite) icon(rel string, size int) string {
	var data bytes.Buffer
	s.Require().NoError(png.Encode(&data, image.NewRGBA(image.Rect(0, 0, size, size))))
	return s.write(rel, data.Bytes())
}
