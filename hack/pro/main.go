package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
)

//go:embed provider.yaml
var provider string

func main() {
	if len(os.Args) < 2 {
		panic("usage: go run main.go <version> [base_path]")
	}

	basePath := "./bin"
	if len(os.Args) > 2 {
		basePath = os.Args[2]
	}

	checksumMap := buildChecksumMap(basePath)

	sourceFile, ok := os.LookupEnv("SOURCE_FILE")
	absPath := loadProviderSource(sourceFile, ok)

	replaced := strings.ReplaceAll(provider, "##VERSION##", os.Args[1])
	replaced = applyChecksums(replaced, checksumMap, os.Getenv("PARTIAL") == "true")

	if !ok {
		fmt.Println(replaced)
		return
	}

	// #nosec G306,G703 -- TODO Consider using a more secure permission setting and ownership if needed.
	if err := os.WriteFile(absPath, []byte(replaced), 0o644); err != nil {
		panic(err)
	}
}

func buildChecksumMap(basePath string) map[string]string {
	bin := config.BinaryName
	return map[string]string{
		filepath.Join(basePath, bin+"-linux-amd64"):       "##CHECKSUM_LINUX_AMD64##",
		filepath.Join(basePath, bin+"-linux-arm64"):       "##CHECKSUM_LINUX_ARM64##",
		filepath.Join(basePath, bin+"-darwin-amd64"):      "##CHECKSUM_DARWIN_AMD64##",
		filepath.Join(basePath, bin+"-darwin-arm64"):      "##CHECKSUM_DARWIN_ARM64##",
		filepath.Join(basePath, bin+"-windows-amd64.exe"): "##CHECKSUM_WINDOWS_AMD64##",
	}
}

// loadProviderSource reads SOURCE_FILE into the package-level provider when
// present and returns its absolute path (empty when unset).
func loadProviderSource(sourceFile string, ok bool) string {
	if !ok {
		return ""
	}

	absPath, err := filepath.Abs(sourceFile)
	if err != nil {
		panic(err)
	}

	providerBytes, err := os.ReadFile(absPath)
	if err != nil {
		panic(err)
	}

	provider = string(providerBytes)
	return absPath
}

func applyChecksums(content string, checksumMap map[string]string, partial bool) string {
	for k, v := range checksumMap {
		checksum, err := File(k)
		if err != nil {
			if partial {
				continue
			}

			panic(fmt.Errorf("generate checksum for %s: %w", k, err))
		}

		content = strings.ReplaceAll(content, v, checksum)
	}
	return content
}

// File hashes a given file to a sha256 string.
func File(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	hash := sha256.New()

	_, err = io.Copy(hash, file)
	if err != nil {
		return "", err
	}

	return strings.ToLower(hex.EncodeToString(hash.Sum(nil))), nil
}
