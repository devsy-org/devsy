package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"math"
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
	fileURLScheme        = "file"
	docsWordmarkWidth    = 1000
	docsWordmarkHeight   = 329
	docsIconCornerRadius = 180
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

func validateRenderingDependencies() error {
	if _, err := findChromeBinary(); err != nil {
		return fmt.Errorf("%w; install Chrome or Chromium before generating icons", err)
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return errors.New("ffmpeg executable not found; install ffmpeg before generating icons")
	}
	return nil
}

func validateSVG(svgPath string) error {
	data, err := os.ReadFile(svgPath)
	if err != nil {
		return fmt.Errorf("read svg: %w", err)
	}
	return validateSVGDocument(data)
}

func validateSVGDocument(data []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	start, err := firstSVGStartElement(decoder)
	if err != nil {
		return err
	}
	if err := validateSVGRoot(start); err != nil {
		return err
	}
	for {
		_, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("parse SVG: %w", err)
		}
	}
}

func firstSVGStartElement(decoder *xml.Decoder) (xml.StartElement, error) {
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return xml.StartElement{}, errors.New("invalid SVG: missing root element")
		}
		if err != nil {
			return xml.StartElement{}, fmt.Errorf("parse SVG: %w", err)
		}
		if start, ok := token.(xml.StartElement); ok {
			return start, nil
		}
	}
}

func validateSVGRoot(start xml.StartElement) error {
	if start.Name.Local != "svg" {
		return fmt.Errorf("invalid SVG root: expected svg, got %s", start.Name.Local)
	}
	for _, attribute := range start.Attr {
		if attribute.Name.Local == "viewBox" && strings.TrimSpace(attribute.Value) != "" {
			return nil
		}
	}
	return errors.New("invalid SVG canvas: missing or empty viewBox attribute")
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

func docsIconPNG(frame []byte) ([]byte, error) {
	source, err := png.Decode(bytes.NewReader(frame))
	if err != nil {
		return nil, fmt.Errorf("decode docs icon: %w", err)
	}
	bounds := source.Bounds()
	if bounds.Dx() != bounds.Dy() {
		return nil, fmt.Errorf("docs icon must be square, got %dx%d", bounds.Dx(), bounds.Dy())
	}

	icon := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := color.NRGBAModel.Convert(source.At(x, y)).(color.NRGBA)
			alpha := roundedSquareAlpha(
				x-bounds.Min.X,
				y-bounds.Min.Y,
				bounds.Dx(),
				docsIconCornerRadius,
			)
			pixel.A = uint8(float64(pixel.A) * alpha)
			icon.SetNRGBA(x, y, pixel)
		}
	}

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, icon); err != nil {
		return nil, fmt.Errorf("encode docs icon: %w", err)
	}
	return encoded.Bytes(), nil
}

func roundedSquareAlpha(x, y, size, radius int) float64 {
	center := float64(radius) - 0.5
	farCenter := float64(size-radius) - 0.5
	px, py := float64(x), float64(y)
	closestX := math.Max(center, math.Min(px, farCenter))
	closestY := math.Max(center, math.Min(py, farCenter))
	distance := math.Hypot(px-closestX, py-closestY)
	return math.Max(0, math.Min(1, float64(radius)+0.5-distance))
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

	return writeDocsIcons(repoRoot, frames)
}

func writeDocsIcons(repoRoot string, frames map[int][]byte) error {
	docsMediaDir := filepath.Join(repoRoot, "sites", "docs-devsy-sh", "public", "docs", "media")
	if _, err := os.Stat(docsMediaDir); err != nil {
		return nil
	}
	transparentDocsIcon, err := docsIconPNG(frames[1024])
	if err != nil {
		return err
	}
	docsIconPath := filepath.Join(docsMediaDir, "devsy-icon.png")
	if err := os.WriteFile(docsIconPath, transparentDocsIcon, 0o644); err != nil {
		return fmt.Errorf("write docs icon: %w", err)
	}
	return writeDocsWordmarks(docsMediaDir, frames[256])
}

//nolint:lll // The fixed vector path data must remain inline in the generated SVG.
func docsWordmarkSVG(textColor string, iconPNG []byte) string {
	iconDataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(iconPNG)
	wordmark := fmt.Sprintf(`<svg width="%d" height="%d"
     viewBox="0 0 %d %d" fill="none" xmlns="http://www.w3.org/2000/svg">
  <title>Devsy</title>
  <defs><clipPath id="app-icon"><rect x="40" y="40" width="249" height="249" rx="56"/></clipPath></defs>
  <image href="%s" x="40" y="40" width="249" height="249" clip-path="url(#app-icon)"/>
  <g fill="%s">
    <path transform="translate(340 215) scale(0.078125 -0.078125)" d="M930 950L930 1556L1114 1556L1114 0L930 0L930 168Q872 68 783.5 19.5Q695 -29 571 -29Q368 -29 240.5 133Q113 295 113 559Q113 823 240.5 985Q368 1147 571 1147Q695 1147 783.5 1098.5Q872 1050 930 950ZM303 559Q303 356 386.5 240.5Q470 125 616 125Q762 125 846 240.5Q930 356 930 559Q930 762 846 877.5Q762 993 616 993Q470 993 386.5 877.5Q303 762 303 559Z"/>
    <path transform="translate(438.563 215) scale(0.078125 -0.078125)" d="M1151 606L1151 516L305 516Q317 326 419.5 226.5Q522 127 705 127Q811 127 910.5 153Q1010 179 1108 231L1108 57Q1009 15 905 -7Q801 -29 694 -29Q426 -29 269.5 127Q113 283 113 549Q113 824 261.5 985.5Q410 1147 662 1147Q888 1147 1019.5 1001.5Q1151 856 1151 606ZM967 660Q965 811 882.5 901Q800 991 664 991Q510 991 417.5 904Q325 817 311 659Z"/>
    <path transform="translate(534 215) scale(0.078125 -0.078125)" d="M61 1120L256 1120L606 180L956 1120L1151 1120L731 0L481 0Z"/>
    <path transform="translate(625.688 215) scale(0.078125 -0.078125)" d="M907 1087L907 913Q829 953 745 973Q661 993 571 993Q434 993 365.5 951Q297 909 297 825Q297 761 346 724.5Q395 688 543 655L606 641Q802 599 884.5 522.5Q967 446 967 309Q967 153 843.5 62Q720 -29 504 -29Q414 -29 316.5 -11.5Q219 6 111 41L111 231Q213 178 312 151.5Q411 125 508 125Q638 125 708 169.5Q778 214 778 295Q778 370 727.5 410Q677 450 506 487L442 502Q271 538 195 612.5Q119 687 119 817Q119 975 231 1061Q343 1147 549 1147Q651 1147 741 1132Q831 1117 907 1087Z"/>
    <path transform="translate(706.047 215) scale(0.078125 -0.078125)" d="M659 -104Q581 -304 507 -365Q433 -426 309 -426L162 -426L162 -272L270 -272Q346 -272 388 -236Q430 -200 481 -66L514 18L61 1120L256 1120L606 244L956 1120L1151 1120Z"/>
  </g>
</svg>
`,
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
	if err := validateRenderingDependencies(); err != nil {
		return err
	}
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
	if len(os.Args) == 2 && os.Args[1] == "--check-dependencies" {
		if err := validateRenderingDependencies(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := run(); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
