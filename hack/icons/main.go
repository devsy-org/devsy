package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	allSizes = []int{16, 24, 32, 48, 64, 128, 256, 512, 1024}
	icoSizes = []int{16, 24, 32, 48, 64, 128, 256}
	icnsTags = []struct {
		tag  [4]byte
		size int
	}{
		{[4]byte{'i', 'c', 'p', '4'}, 16},
		{[4]byte{'i', 'c', 'p', '5'}, 32},
		{[4]byte{'i', 'c', 'p', '6'}, 64},
		{[4]byte{'i', 'c', '0', '7'}, 128},
		{[4]byte{'i', 'c', '0', '8'}, 256},
		{[4]byte{'i', 'c', '0', '9'}, 512},
		{[4]byte{'i', 'c', '1', '0'}, 1024},
		{[4]byte{'i', 'c', '1', '1'}, 32},
		{[4]byte{'i', 'c', '1', '2'}, 64},
		{[4]byte{'i', 'c', '1', '3'}, 256},
		{[4]byte{'i', 'c', '1', '4'}, 512},
	}
)

const (
	fileURLScheme      = "file"
	docsWordmarkWidth  = 1000
	docsWordmarkHeight = 329
)

func findChromeBinary() (string, error) {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".cache", "ms-playwright", "chromium-1234", "chrome-linux64", "chrome"),
		"/usr/bin/google-chrome",
		"/usr/bin/chromium-browser",
		"/usr/bin/chromium",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	for _, name := range []string{"chrome", "chromium"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errors.New("chromium or chrome executable not found")
}

func validateSVG(svgPath string) error {
	data, err := os.ReadFile(svgPath)
	if err != nil {
		return fmt.Errorf("read svg: %w", err)
	}
	content := string(data)
	if !strings.Contains(content, "<svg") || !strings.Contains(content, "viewBox=") {
		return fmt.Errorf("invalid SVG canvas: must contain <svg and viewBox attribute")
	}
	return nil
}

func renderPageContents(svgPath string, width, height int) string {
	svgURL := (&url.URL{Scheme: fileURLScheme, Path: svgPath}).String()
	return fmt.Sprintf(`<!doctype html>
<html><head><style>
html, body, img { width: %dpx; height: %dpx; margin: 0; padding: 0; overflow: hidden; }
img { display: block; }
</style></head><body><img src="%s"></body></html>`, width, height, svgURL)
}

func renderSVGPNG(svgPath, outPNG string, width, height int) error {
	chrome, err := findChromeBinary()
	if err != nil {
		return err
	}
	absSVG, err := filepath.Abs(svgPath)
	if err != nil {
		return err
	}
	renderPage := outPNG + ".html"
	pageContents := renderPageContents(absSVG, width, height)
	if err := os.WriteFile(renderPage, []byte(pageContents), 0o644); err != nil {
		return fmt.Errorf("write SVG render page: %w", err)
	}
	defer func() { _ = os.Remove(renderPage) }()

	cmd := exec.Command(chrome,
		"--headless",
		"--no-sandbox",
		"--allow-file-access-from-files",
		"--virtual-time-budget=1000",
		"--default-background-color=00000000",
		fmt.Sprintf("--screenshot=%s", outPNG),
		fmt.Sprintf("--window-size=%d,%d", width, height),
		(&url.URL{Scheme: fileURLScheme, Path: renderPage}).String(),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("render svg with chrome: %w, output: %s", err, string(out))
	}
	if _, err := os.Stat(outPNG); err != nil {
		return fmt.Errorf("rendered png missing: %w", err)
	}
	return nil
}

func renderMasterPNG(svgPath, outPNG string) error {
	return renderSVGPNG(svgPath, outPNG, 1024, 1024)
}

func resizePNG(inPNG, outPNG string, size int) error {
	cmd := exec.Command("ffmpeg",
		"-y",
		"-i", inPNG,
		"-vf", fmt.Sprintf("scale=%d:%d", size, size),
		"-update", "1",
		outPNG,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("resize to %dx%d: %w, output: %s", size, size, err, string(out))
	}
	return nil
}

func writeICNSChunk(body *bytes.Buffer, tag [4]byte, data []byte) error {
	if err := binary.Write(body, binary.BigEndian, tag); err != nil {
		return err
	}
	chunkLen := uint32(len(data) + 8)
	if err := binary.Write(body, binary.BigEndian, chunkLen); err != nil {
		return err
	}
	_, err := body.Write(data)
	return err
}

func packICNS(frames map[int][]byte) ([]byte, error) {
	var body bytes.Buffer
	for _, entry := range icnsTags {
		data, ok := frames[entry.size]
		if !ok || len(data) == 0 {
			return nil, fmt.Errorf("missing frame for size %d", entry.size)
		}
		if err := writeICNSChunk(&body, entry.tag, data); err != nil {
			return nil, err
		}
	}

	var result bytes.Buffer
	magic := [4]byte{'i', 'c', 'n', 's'}
	if err := binary.Write(&result, binary.BigEndian, magic); err != nil {
		return nil, err
	}
	totalLen := uint32(body.Len() + 8)
	if err := binary.Write(&result, binary.BigEndian, totalLen); err != nil {
		return nil, err
	}
	if _, err := result.Write(body.Bytes()); err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}

type icoDirEntry struct {
	Width       byte
	Height      byte
	ColorCount  byte
	Reserved    byte
	Planes      uint16
	BitCount    uint16
	BytesInRes  uint32
	ImageOffset uint32
}

func writeICOEntry(buf *bytes.Buffer, size int, dataLen, offset uint32) error {
	w := byte(size)
	if size == 256 {
		w = 0
	}
	entry := icoDirEntry{
		Width:       w,
		Height:      w,
		Planes:      1,
		BitCount:    32,
		BytesInRes:  dataLen,
		ImageOffset: offset,
	}
	return binary.Write(buf, binary.LittleEndian, entry)
}

func writeICOHeader(buf *bytes.Buffer, count uint16) error {
	if err := binary.Write(buf, binary.LittleEndian, uint16(0)); err != nil {
		return err
	}
	if err := binary.Write(buf, binary.LittleEndian, uint16(1)); err != nil {
		return err
	}
	return binary.Write(buf, binary.LittleEndian, count)
}

func packICO(frames map[int][]byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeICOHeader(&buf, uint16(len(icoSizes))); err != nil {
		return nil, err
	}

	offset := uint32(6 + 16*len(icoSizes))
	var imgData bytes.Buffer

	for _, s := range icoSizes {
		data, ok := frames[s]
		if !ok || len(data) == 0 {
			return nil, fmt.Errorf("missing frame for ico size %d", s)
		}
		if err := writeICOEntry(&buf, s, uint32(len(data)), offset); err != nil {
			return nil, err
		}
		imgData.Write(data)
		offset += uint32(len(data))
	}

	buf.Write(imgData.Bytes())
	return buf.Bytes(), nil
}

func resolveSVGPath(resourcesDir string) string {
	svgPath := filepath.Join(resourcesDir, "icon.svg")
	if _, err := os.Stat(svgPath); err != nil {
		return filepath.Join(resourcesDir, "box-icon-app.svg")
	}
	return svgPath
}

func renderAllFrames(svgPath, tmpDir string) (map[int][]byte, error) {
	masterPNG := filepath.Join(tmpDir, "master-1024.png")
	log.Println("Rendering 1024x1024 master PNG using headless browser...")
	if err := renderMasterPNG(svgPath, masterPNG); err != nil {
		return nil, err
	}

	log.Println("Rescaling icon resolutions...")
	frames := make(map[int][]byte)
	masterBytes, err := os.ReadFile(masterPNG)
	if err != nil {
		return nil, err
	}
	frames[1024] = masterBytes

	for _, s := range allSizes {
		if s == 1024 {
			continue
		}
		outPNG := filepath.Join(tmpDir, fmt.Sprintf("%dx%d.png", s, s))
		if err := resizePNG(masterPNG, outPNG, s); err != nil {
			return nil, err
		}
		b, err := os.ReadFile(outPNG)
		if err != nil {
			return nil, err
		}
		frames[s] = b
	}
	return frames, nil
}

func writeMacIcons(resourcesDir string, frames map[int][]byte) error {
	log.Println("Packing macOS .icns...")
	icnsData, err := packICNS(frames)
	if err != nil {
		return err
	}
	icnsDst := filepath.Join(resourcesDir, "icon.icns")
	if err := os.WriteFile(icnsDst, icnsData, 0o644); err != nil {
		return err
	}
	log.Printf("✓ Updated %s (%d bytes)", icnsDst, len(icnsData))
	return nil
}

func writeWindowsIcons(resourcesDir string, frames map[int][]byte) error {
	log.Println("Packing Windows .ico...")
	icoData, err := packICO(frames)
	if err != nil {
		return err
	}
	icoDst := filepath.Join(resourcesDir, "icon.ico")
	if err := os.WriteFile(icoDst, icoData, 0o644); err != nil {
		return err
	}
	log.Printf("✓ Updated %s (%d bytes)", icoDst, len(icoData))
	return nil
}

func writeLinuxIcons(resourcesDir, repoRoot string, frames map[int][]byte) error {
	log.Println("Updating Linux icons and master icon.png...")
	pngDst := filepath.Join(resourcesDir, "icon.png")
	if err := os.WriteFile(pngDst, frames[1024], 0o644); err != nil {
		return err
	}
	iconsDir := filepath.Join(resourcesDir, "icons")
	if err := os.MkdirAll(iconsDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(iconsDir, "32x32.png"), frames[32], 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(iconsDir, "128x128.png"), frames[128], 0o644); err != nil {
		return err
	}
	log.Println("✓ Updated Linux icons (32x32, 128x128) and icon.png")

	docsMediaDir := filepath.Join(
		repoRoot,
		"sites",
		"docs-devsy-sh",
		"public",
		"docs",
		"media",
	)
	if _, err := os.Stat(docsMediaDir); err == nil {
		docsIconPNG := filepath.Join(docsMediaDir, "devsy-icon.png")
		if err := os.WriteFile(docsIconPNG, frames[1024], 0o644); err != nil {
			return fmt.Errorf("write docs icon: %w", err)
		}
		if err := writeDocsWordmarks(docsMediaDir, frames[256]); err != nil {
			return err
		}
	}
	return nil
}

func docsWordmarkSVG(textColor string, iconPNG []byte) string {
	iconDataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(iconPNG)
	wordmark := fmt.Sprintf(`<svg width="%d" height="%d"
     viewBox="0 0 %d %d" fill="none" xmlns="http://www.w3.org/2000/svg">
  <title>Devsy</title>
  <defs><clipPath id="app-icon"><rect x="40" y="40" width="249" height="249" rx="56"/></clipPath></defs>
  <image href="%s" x="40" y="40" width="249" height="249" clip-path="url(#app-icon)"/>
  <text x="340" y="215" font-family="Inter, 'Helvetica Neue', Arial, sans-serif"
        font-size="160" font-weight="600" letter-spacing="-6" fill="%s">devsy</text>
</svg>`,
		docsWordmarkWidth,
		docsWordmarkHeight,
		docsWordmarkWidth,
		docsWordmarkHeight,
		iconDataURL,
		textColor,
	)
	return wordmark
}

func writeDocsWordmarks(docsMediaDir string, iconPNG []byte) error {
	variants := []struct {
		svgName   string
		pngName   string
		textColor string
	}{
		{"devsy-logo-horizontal.svg", "devsy.png", "#0B0B14"},
		{"devsy-logo-horizontal-dark.svg", "devsy-dark.png", "#FFFFFF"},
	}

	for _, variant := range variants {
		svgPath := filepath.Join(docsMediaDir, variant.svgName)
		wordmarkSVG := docsWordmarkSVG(variant.textColor, iconPNG)
		if err := os.WriteFile(svgPath, []byte(wordmarkSVG), 0o644); err != nil {
			return fmt.Errorf("write docs wordmark SVG: %w", err)
		}
		pngPath := filepath.Join(docsMediaDir, variant.pngName)
		err := renderSVGPNG(svgPath, pngPath, docsWordmarkWidth, docsWordmarkHeight)
		if err != nil {
			return fmt.Errorf("render docs wordmark PNG: %w", err)
		}
	}
	return nil
}

func syncSVGDuplicates(svgPath, resourcesDir string) error {
	boxSVG := filepath.Join(resourcesDir, "box-icon-app.svg")
	if svgPath != boxSVG {
		data, err := os.ReadFile(svgPath)
		if err != nil {
			return fmt.Errorf("read canonical svg: %w", err)
		}
		if err := os.WriteFile(boxSVG, data, 0o644); err != nil {
			return fmt.Errorf("sync box-icon-app.svg: %w", err)
		}
	}
	return nil
}

func writeAllAssets(resourcesDir, repoRoot, svgPath string, frames map[int][]byte) error {
	if err := writeMacIcons(resourcesDir, frames); err != nil {
		return err
	}
	if err := writeWindowsIcons(resourcesDir, frames); err != nil {
		return err
	}
	if err := writeLinuxIcons(resourcesDir, repoRoot, frames); err != nil {
		return err
	}
	return syncSVGDuplicates(svgPath, resourcesDir)
}

func run() error {
	repoRoot, err := filepath.Abs(".")
	if err != nil {
		return err
	}
	resourcesDir := filepath.Join(repoRoot, "desktop", "resources")
	svgPath := resolveSVGPath(resourcesDir)

	log.Printf("Validating source SVG: %s", svgPath)
	if err := validateSVG(svgPath); err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp("", "devsy-icons-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	frames, err := renderAllFrames(svgPath, tmpDir)
	if err != nil {
		return err
	}
	if err := writeAllAssets(resourcesDir, repoRoot, svgPath, frames); err != nil {
		return err
	}

	log.Println("=== Icon generation complete! ===")
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
