package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

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

const goodEntry = "[Desktop Entry]\nName=Devsy\nIcon=sh.devsy.app\nStartupWMClass=sh.devsy.app\n"

func (s *VerifyLinuxDesktopSuite) TestAcceptsConsistentPackage() {
	entry := s.write("share/applications/sh.devsy.app.desktop", goodEntry)
	s.write("share/icons/hicolor/128x128/apps/sh.devsy.app.png", "png")
	runtimeIcon := s.write("resources/app-icon.png", "png")

	s.Require().NoError(verifyLinuxDesktop("sh.devsy.app", entry, s.root, []string{runtimeIcon}))
}

func (s *VerifyLinuxDesktopSuite) TestRejectsWrongFilename() {
	entry := s.write("devsy.desktop", goodEntry)
	s.write("sh.devsy.app.png", "png")

	s.Require().
		ErrorContains(verifyLinuxDesktop("sh.devsy.app", entry, s.root, nil), "expected sh.devsy.app.desktop")
}

func (s *VerifyLinuxDesktopSuite) TestRejectsMismatchedWindowClass() {
	entry := s.write(
		"sh.devsy.app.desktop",
		"[Desktop Entry]\nIcon=sh.devsy.app\nStartupWMClass=Devsy\n",
	)
	s.write("sh.devsy.app.png", "png")

	s.Require().
		ErrorContains(verifyLinuxDesktop("sh.devsy.app", entry, s.root, nil), "StartupWMClass")
}

func (s *VerifyLinuxDesktopSuite) TestIgnoresKeysOutsideDesktopEntryGroup() {
	entry := s.write("sh.devsy.app.desktop",
		"[Desktop Entry]\nIcon=sh.devsy.app\n[Desktop Action x]\nStartupWMClass=sh.devsy.app\n")
	s.write("sh.devsy.app.png", "png")

	s.Require().
		ErrorContains(verifyLinuxDesktop("sh.devsy.app", entry, s.root, nil), "StartupWMClass")
}

func (s *VerifyLinuxDesktopSuite) TestRejectsMissingOrEmptyIcon() {
	entry := s.write("sh.devsy.app.desktop", goodEntry)
	s.Require().ErrorContains(verifyLinuxDesktop("sh.devsy.app", entry, s.root, nil), "not found")

	s.write("icons/sh.devsy.app.png", "")
	s.Require().ErrorContains(verifyLinuxDesktop("sh.devsy.app", entry, s.root, nil), "not found")
}

func (s *VerifyLinuxDesktopSuite) TestRejectsMissingRequiredFile() {
	entry := s.write("sh.devsy.app.desktop", goodEntry)
	s.write("sh.devsy.app.png", "png")

	s.Require().
		Error(verifyLinuxDesktop("sh.devsy.app", entry, s.root, []string{filepath.Join(s.root, "absent.png")}))
}

func (s *VerifyLinuxDesktopSuite) write(rel, content string) string {
	path := filepath.Join(s.root, rel)
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o750))
	s.Require().NoError(os.WriteFile(path, []byte(content), 0o600))
	return path
}
