// Generates the Homebrew cask for the Devsy desktop app from the released
// per-arch macOS disk images, embedding each image's sha256.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

type arch struct {
	Cask   string // cask arch token: arm, intel
	DMG    string // release asset filename
	SHA256 string
}

var arches = []arch{
	{Cask: "arm", DMG: "Devsy_mac_arm64.dmg"},
	{Cask: "intel", DMG: "Devsy_mac_x64.dmg"},
}

const caskTmpl = `cask "devsy" do
  arch arm: "arm64", intel: "x64"

  version "{{ .Version }}"
  sha256 arm:   "{{ (index .Arches "arm").SHA256 }}",
         intel: "{{ (index .Arches "intel").SHA256 }}"

  url "https://github.com/{{ .Repo }}/releases/download/v#{version}/Devsy_mac_#{arch}.dmg"
  name "Devsy"
  desc "Standardized dev workspaces across Docker, Kubernetes, cloud, and SSH"
  homepage "https://www.devsy.sh"

  app "Devsy.app"

  zap trash: [
    "~/Library/Application Support/Devsy",
    "~/Library/Logs/Devsy",
    "~/Library/Preferences/sh.devsy.app.plist",
    "~/Library/Saved Application State/sh.devsy.app.savedState",
  ]
end
`

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- release tooling reads local build artifacts.
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func render(dmgDir, repo, tag string) (string, error) {
	byKey := make(map[string]arch, len(arches))
	for _, a := range arches {
		sum, err := fileSHA256(filepath.Join(dmgDir, a.DMG))
		if err != nil {
			return "", fmt.Errorf("checksum %s: %w", a.DMG, err)
		}
		a.SHA256 = sum
		byKey[a.Cask] = a
	}

	tmpl, err := template.New("cask").Parse(caskTmpl)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var sb strings.Builder
	data := struct {
		Version string
		Repo    string
		Arches  map[string]arch
	}{Version: strings.TrimPrefix(tag, "v"), Repo: repo, Arches: byKey}
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("render template: %w", err)
	}
	return sb.String(), nil
}

func main() {
	dmgDir := flag.String(
		"dmg-dir",
		"",
		"directory containing the Devsy_mac_<arch>.dmg release images",
	)
	repo := flag.String("repo", "", "owner/repo the release assets are published under")
	tag := flag.String("tag", "", "release tag (e.g. v1.2.3)")
	out := flag.String("out", "", "path to write the generated cask")
	flag.Parse()

	if *dmgDir == "" || *repo == "" || *tag == "" || *out == "" {
		fmt.Fprintln(
			os.Stderr,
			"usage: homebrew_cask --dmg-dir DIR --repo OWNER/REPO --tag TAG --out FILE",
		)
		os.Exit(2)
	}

	cask, err := render(*dmgDir, *repo, *tag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// #nosec G306 -- cask is public.
	if err := os.WriteFile(*out, []byte(cask), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", *out)
}
