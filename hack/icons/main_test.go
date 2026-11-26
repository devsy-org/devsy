package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func dummyPNG() []byte {
	return []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01, 0x02, 0x03}
}

func parseICNSTags(data []byte) []string {
	offset := 8
	var found []string
	for offset < len(data) {
		tag := string(data[offset : offset+4])
		chunkLen := binary.BigEndian.Uint32(data[offset+4 : offset+8])
		found = append(found, tag)
		offset += int(chunkLen)
	}
	return found
}

func TestPackICNS(t *testing.T) {
	frames := make(map[int][]byte)
	for _, entry := range icnsTags {
		frames[entry.size] = dummyPNG()
	}

	data, err := packICNS(frames)
	if err != nil {
		t.Fatalf("packICNS failed: %v", err)
	}

	if len(data) < 8 || string(data[:4]) != "icns" {
		t.Fatalf("invalid icns header")
	}

	totalLen := binary.BigEndian.Uint32(data[4:8])
	if int(totalLen) != len(data) {
		t.Errorf("expected length %d, got %d", len(data), totalLen)
	}

	foundTags := parseICNSTags(data)
	expectedTags := []string{
		"icp4", "icp5", "icp6", "ic07", "ic08", "ic09",
		"ic10", "ic11", "ic12", "ic13", "ic14",
	}
	for _, expected := range expectedTags {
		if !slices.Contains(foundTags, expected) {
			t.Errorf("missing expected ICNS tag: %s", expected)
		}
	}
}

func checkICOEntry(t *testing.T, data []byte, idx, size int) {
	entryOffset := 6 + idx*16
	w := int(data[entryOffset])
	if size == 256 && w != 0 {
		t.Errorf("entry %d: expected 0 for width 256, got %d", idx, w)
	} else if size != 256 && w != size {
		t.Errorf("entry %d: expected width %d, got %d", idx, size, w)
	}

	imgSize := binary.LittleEndian.Uint32(data[entryOffset+8 : entryOffset+12])
	imgOffset := binary.LittleEndian.Uint32(data[entryOffset+12 : entryOffset+16])

	imgData := data[imgOffset : imgOffset+imgSize]
	if !bytes.HasPrefix(imgData, []byte{0x89, 0x50, 0x4e, 0x47}) {
		t.Errorf("entry %d: image data at %d is not PNG", idx, imgOffset)
	}
}

func TestPackICO(t *testing.T) {
	frames := make(map[int][]byte)
	for _, s := range icoSizes {
		frames[s] = dummyPNG()
	}

	data, err := packICO(frames)
	if err != nil {
		t.Fatalf("packICO failed: %v", err)
	}

	if len(data) < 6 {
		t.Fatalf("ico too short: %d", len(data))
	}

	reserved := binary.LittleEndian.Uint16(data[0:2])
	icoType := binary.LittleEndian.Uint16(data[2:4])
	count := binary.LittleEndian.Uint16(data[4:6])

	if reserved != 0 || icoType != 1 || int(count) != len(icoSizes) {
		t.Errorf("invalid ico header: reserved=%d, type=%d, count=%d", reserved, icoType, count)
	}

	for i, s := range icoSizes {
		checkICOEntry(t, data, i, s)
	}
}

func TestValidateSVG(t *testing.T) {
	tmpDir := t.TempDir()

	validSVG := filepath.Join(tmpDir, "valid.svg")
	validContent := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024"></svg>`)
	if err := os.WriteFile(validSVG, validContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateSVG(validSVG); err != nil {
		t.Errorf("expected valid SVG to pass, got: %v", err)
	}

	invalidSVG := filepath.Join(tmpDir, "invalid.svg")
	if err := os.WriteFile(
		invalidSVG,
		[]byte(`<html><body>not svg</body></html>`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := validateSVG(invalidSVG); err == nil {
		t.Error("expected invalid SVG to fail")
	}

	for name, content := range map[string]string{
		"missing-viewbox": `<svg xmlns="http://www.w3.org/2000/svg"></svg>`,
		"empty-viewbox":   `<svg viewBox=" "></svg>`,
		"wrong-root":      `<html viewBox="0 0 1 1"></html>`,
		"malformed":       `<svg viewBox="0 0 1 1">`,
	} {
		path := filepath.Join(tmpDir, name+".svg")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := validateSVG(path); err == nil {
			t.Errorf("expected %s SVG to fail", name)
		}
	}
}

func TestValidateRenderingDependencies(t *testing.T) {
	if err := validateRenderingDependencies(); err != nil {
		t.Skipf("icon rendering dependencies are unavailable: %v", err)
	}
}

func TestRenderPageContentsScalesSVGToCanvas(t *testing.T) {
	svgPath := "/tmp/large icon.svg"
	page := renderPageContents(svgPath, 1024, 1024)

	expectedURL := (&url.URL{Scheme: fileURLScheme, Path: svgPath}).String()
	if !strings.Contains(page, `width: 1024px; height: 1024px`) {
		t.Error("render page does not set a 1024x1024 canvas")
	}
	if !strings.Contains(page, `img src="`+expectedURL+`"`) {
		t.Errorf("render page does not reference the SVG: %s", page)
	}
}

const brandViolet = "#7C5CFF"

func TestDocsWordmarkSVG(t *testing.T) {
	wordmark := docsWordmarkSVG("#FFFFFF", `<path d="M0 0"/>`)
	if !strings.Contains(wordmark, `<path d="M0 0"/>`) {
		t.Error("wordmark does not embed the application icon drawing")
	}
	if !strings.Contains(wordmark, `fill="#FFFFFF"`) {
		t.Error("wordmark does not use its requested text color")
	}
	if strings.Contains(wordmark, "<text") || strings.Contains(wordmark, "font-family") {
		t.Error("wordmark must use fixed vector paths instead of a system font")
	}
	if strings.Contains(wordmark, "<image") || strings.Contains(wordmark, "base64") {
		t.Error("wordmark must not embed a raster image")
	}
}

func TestIconMarkupReturnsRootContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "icon.svg")
	src := "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 8 8\">\n  <path d=\"M0 0\"/>\n</svg>\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := iconMarkup(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != `<path d="M0 0"/>` {
		t.Errorf("unexpected markup %q", got)
	}
}

func repoFile(parts ...string) string {
	return filepath.Join(append([]string{"..", ".."}, parts...)...)
}

func TestShippedSVGsAreVectorOnly(t *testing.T) {
	media := repoFile("sites", "docs-devsy-sh", "public", "docs", "media")
	paths := []string{
		repoFile("desktop", "resources", "icon.svg"),
		repoFile("desktop", "resources", "box-icon-app.svg"),
		repoFile("sites", "docs-devsy-sh", "app", "icon.svg"),
		filepath.Join(media, "devsy-favicon.svg"),
		filepath.Join(media, "devsy-logo-horizontal.svg"),
		filepath.Join(media, "devsy-logo-horizontal-dark.svg"),
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		src := strings.ToLower(string(data))
		for _, banned := range []string{"<image", "data:", "base64", "c2pa", "openai"} {
			if strings.Contains(src, banned) {
				t.Errorf("%s contains %q", path, banned)
			}
		}
		if len(data) > 8*1024 {
			t.Errorf("%s is %d bytes, want at most 8 KiB", path, len(data))
		}
	}
}

func TestIconSourceKeepsBrandViolet(t *testing.T) {
	data, err := os.ReadFile(repoFile("desktop", "resources", "icon.svg"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToUpper(string(data)), brandViolet) {
		t.Errorf("icon.svg does not use %s", brandViolet)
	}
}

func decodeScaledSquare(t *testing.T, size int) image.Image {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	for y := 64; y < 192; y++ {
		for x := 64; x < 192; x++ {
			source.SetNRGBA(x, y, testArt)
		}
	}
	data, err := scaleFrame(source, size)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func nearArt(p color.NRGBA) bool {
	return absDiff(p.R, testArt.R) <= 12 && absDiff(p.B, testArt.B) <= 12
}

func TestScaleFrameKeepsColorAtTransparentEdges(t *testing.T) {
	decoded := decodeScaledSquare(t, 16)
	if got := color.NRGBAModel.Convert(decoded.At(0, 0)).(color.NRGBA).A; got != 0 {
		t.Errorf("corner alpha is %d, want 0", got)
	}
	if got := color.NRGBAModel.Convert(decoded.At(8, 8)); got != testArt {
		t.Errorf("interior is %v, want %v", got, testArt)
	}
	for y := range 16 {
		for x := range 16 {
			p := color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA)
			if p.A > 0 && !nearArt(p) {
				t.Fatalf("pixel %d,%d is %v, edge color must stay %v", x, y, p, testArt)
			}
		}
	}
}

func absDiff(a, b uint8) int {
	return max(int(a)-int(b), int(b)-int(a))
}

func TestMasterIconHasTransparentCornersAndVioletBody(t *testing.T) {
	data, err := os.ReadFile(repoFile("desktop", "resources", "icon.png"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if color.NRGBAModel.Convert(decoded.At(0, 0)).(color.NRGBA).A != 0 {
		t.Error("master icon corner must be transparent")
	}
	want := color.NRGBA{R: 0x7C, G: 0x5C, B: 0xFF, A: 255}
	if got := color.NRGBAModel.Convert(decoded.At(120, 512)); got != want {
		t.Errorf("icon body is %v, want %v", got, want)
	}
}

var testArt = color.NRGBA{R: 110, G: 80, B: 250, A: 255}

func TestSmallDocsIconIsSmallAndTransparent(t *testing.T) {
	path := filepath.Join(
		"..", "..", "sites", "docs-devsy-sh", "public", "docs", "media", "devsy-icon-128.png",
	)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 40*1024 {
		t.Errorf("small docs icon is %d bytes, want at most 40 KiB", len(data))
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded.Bounds().Dx(); got != 128 {
		t.Errorf("small docs icon is %d px wide, want 128", got)
	}
	if color.NRGBAModel.Convert(decoded.At(0, 0)).(color.NRGBA).A != 0 {
		t.Error("small docs icon corner must be transparent")
	}
}
