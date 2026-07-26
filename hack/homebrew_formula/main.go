// Generates the Homebrew formula for the Devsy CLI from the released
// per-platform binaries, embedding each asset's release URL and sha256.
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

type platform struct {
	OS     string // Homebrew block: macos, linux
	Arch   string // Homebrew block: arm, intel
	Binary string // release asset filename
	URL    string
	SHA256 string
}

var platforms = []platform{
	{OS: "macos", Arch: "arm", Binary: "devsy-darwin-arm64"},
	{OS: "macos", Arch: "intel", Binary: "devsy-darwin-amd64"},
	{OS: "linux", Arch: "arm", Binary: "devsy-linux-arm64"},
	{OS: "linux", Arch: "intel", Binary: "devsy-linux-amd64"},
}

const formulaTmpl = `class Devsy < Formula
  desc "Standardized dev workspaces across Docker, Kubernetes, cloud, and SSH"
  homepage "https://www.devsy.sh"
  version "{{ .Version }}"
  license "MPL-2.0"

  on_macos do
    on_arm do
      url "{{ (index .Platforms "macos/arm").URL }}"
      sha256 "{{ (index .Platforms "macos/arm").SHA256 }}"
    end
    on_intel do
      url "{{ (index .Platforms "macos/intel").URL }}"
      sha256 "{{ (index .Platforms "macos/intel").SHA256 }}"
    end
  end

  on_linux do
    on_arm do
      url "{{ (index .Platforms "linux/arm").URL }}"
      sha256 "{{ (index .Platforms "linux/arm").SHA256 }}"
    end
    on_intel do
      url "{{ (index .Platforms "linux/intel").URL }}"
      sha256 "{{ (index .Platforms "linux/intel").SHA256 }}"
    end
  end

  def install
    bin.install Dir["devsy-*"].first => "devsy"
  end

  test do
    system "#{bin}/devsy", "--version"
  end
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

func assetURL(repo, tag, filename string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, tag, filename)
}

func render(binDir, repo, tag string) (string, error) {
	byKey := make(map[string]platform, len(platforms))
	for _, p := range platforms {
		sum, err := fileSHA256(filepath.Join(binDir, p.Binary))
		if err != nil {
			return "", fmt.Errorf("checksum %s: %w", p.Binary, err)
		}
		p.URL = assetURL(repo, tag, p.Binary)
		p.SHA256 = sum
		byKey[p.OS+"/"+p.Arch] = p
	}

	tmpl, err := template.New("formula").Parse(formulaTmpl)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var sb strings.Builder
	data := struct {
		Version   string
		Platforms map[string]platform
	}{Version: strings.TrimPrefix(tag, "v"), Platforms: byKey}
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("render template: %w", err)
	}
	return sb.String(), nil
}

func main() {
	binDir := flag.String("bin-dir", "", "directory containing the devsy-<os>-<arch> release binaries")
	repo := flag.String("repo", "", "owner/repo the release assets are published under")
	tag := flag.String("tag", "", "release tag (e.g. v1.2.3)")
	out := flag.String("out", "", "path to write the generated formula")
	flag.Parse()

	if *binDir == "" || *repo == "" || *tag == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: homebrew_formula --bin-dir DIR --repo OWNER/REPO --tag TAG --out FILE")
		os.Exit(2)
	}

	formula, err := render(*binDir, *repo, *tag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, []byte(formula), 0o644); err != nil { // #nosec G306 -- formula is public.
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", *out)
}
