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

const fakeChromeEnv = "DEVSY_ICONS_FAKE_CHROME_PNG"

// TestMain lets the test binary stand in for Chrome: it copies the PNG named by
// fakeChromeEnv to the --screenshot path.
func TestMain(m *testing.M) {
	if source := os.Getenv(fakeChromeEnv); source != "" {
		os.Exit(runFakeChrome(source, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func runFakeChrome(source string, args []string) int {
	data, err := os.ReadFile(source)
	if err != nil {
		return 1
	}
	for _, arg := range args {
		if out, ok := strings.CutPrefix(arg, "--screenshot="); ok {
			if err := os.WriteFile(out, data, 0o644); err != nil {
				return 1
			}
			return 0
		}
	}
	return 1
}

func useFakeChrome(t *testing.T, screenshot string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(chromeEnv, exe)
	t.Setenv(fakeChromeEnv, screenshot)
}

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

const (
	svgRoot    = `<svg xmlns="` + svgNamespace + `" viewBox="0 0 1 1"></svg>`
	svgOpen    = `<svg xmlns="` + svgNamespace + `"`
	errParse   = "parse SVG"
	errNoRoot  = "missing root element"
	errOutside = "text outside the root element"
	errNS      = "missing xmlns"
	errCanvas  = "viewBox"
)

func TestValidateSVGAcceptsValidDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "valid.svg")
	if err := os.WriteFile(path, []byte(svgRoot), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateSVG(path); err != nil {
		t.Errorf("expected valid SVG to pass, got: %v", err)
	}
}

// Each case carries a valid namespace unless the namespace is what it tests, so
// the error names the check under test and not the first check that fails.
func TestValidateSVGRejectsEachDefect(t *testing.T) {
	cases := map[string]struct{ content, want string }{
		"missing-viewbox": {svgOpen + `></svg>`, errCanvas},
		"empty-viewbox":   {svgOpen + ` viewBox=" "></svg>`, errCanvas},
		"wrong-root": {
			`<html xmlns="` + svgNamespace + `" viewBox="0 0 1 1"><body/></html>`,
			"expected svg",
		},
		"malformed":    {svgOpen + ` viewBox="0 0 1 1">`, errParse},
		"comment-only": {`<!-- ` + svgRoot + ` -->`, errNoRoot},
		"text-only":    {"not an svg at all", errOutside},
		"empty-file":   {"", errNoRoot},
		"text-before":  {"junk" + svgRoot, errOutside},
		"text-after":   {svgRoot + "junk", errOutside},
		"second-root":  {svgRoot + svgRoot, "more than one root"},
		"no-xmlns":     {`<svg viewBox="0 0 1 1"></svg>`, errNS},
		"wrong-xmlns":  {`<svg xmlns="http://example.com/svg" viewBox="0 0 1 1"></svg>`, errNS},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "case.svg")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			err := validateSVG(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("validateSVG error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestValidateSVGDocumentAcceptsPlainDocuments(t *testing.T) {
	root := `<svg xmlns="` + svgNamespace + `" viewBox="0 0 1 1"><g><path d="M0 0"/></g></svg>`
	for name, content := range map[string]string{
		"plain":           root,
		"prolog":          "<?xml version=\"1.0\"?>\n" + root + "\n",
		"comment-before":  "<!-- note -->\n" + root,
		"byte-order-mark": "\xef\xbb\xbf" + root,
		"whitespace":      "\n\t " + root + " \n",
	} {
		if err := validateSVGDocument([]byte(content)); err != nil {
			t.Errorf("%s: expected the document to pass, got: %v", name, err)
		}
	}
}

func TestValidateSVGRejectsShippedIconWithoutNamespace(t *testing.T) {
	data, err := os.ReadFile(repoFile("desktop", "resources", "icon.svg"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSVGDocument(data); err != nil {
		t.Fatalf("shipped icon rejected: %v", err)
	}
	stripped := strings.Replace(string(data), `xmlns="`+svgNamespace+`" `, "", 1)
	if stripped == string(data) {
		t.Fatal("shipped icon has no xmlns attribute to strip")
	}
	if err := validateSVGDocument([]byte(stripped)); err == nil {
		t.Error("expected the icon without xmlns to be rejected before rendering")
	}
}

func writeTestPNG(t *testing.T, img image.Image) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "render.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerifyRenderedPNG(t *testing.T) {
	visible := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	visible.SetNRGBA(1, 1, color.NRGBA{R: 124, G: 92, B: 255, A: 255})
	if err := verifyRenderedPNG(writeTestPNG(t, visible), 4, 2); err != nil {
		t.Errorf("expected a visible render to pass, got: %v", err)
	}
	for name, size := range map[string][2]int{"both": {8, 4}, "width": {8, 2}, "height": {4, 4}} {
		if err := verifyRenderedPNG(writeTestPNG(t, visible), size[0], size[1]); err == nil {
			t.Errorf("expected a render with the wrong %s to fail", name)
		}
	}
	faint := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	faint.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 1})
	if err := verifyRenderedPNG(writeTestPNG(t, faint), 4, 2); err != nil {
		t.Errorf("expected a partly transparent render to pass, got: %v", err)
	}
	blank := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	if err := verifyRenderedPNG(writeTestPNG(t, blank), 4, 2); err == nil {
		t.Error("expected a fully transparent render to fail")
	}
}

func TestVerifyRenderedPNGRejectsUnreadableFiles(t *testing.T) {
	notPNG := filepath.Join(t.TempDir(), "bad.png")
	if err := os.WriteFile(notPNG, []byte("not a png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyRenderedPNG(notPNG, 4, 2); err == nil {
		t.Error("expected a corrupt render to fail")
	}
	if err := verifyRenderedPNG(filepath.Join(t.TempDir(), "missing.png"), 4, 2); err == nil {
		t.Error("expected a missing render to fail")
	}
}

func TestRenderSVGPNGRejectsBadScreenshots(t *testing.T) {
	visible := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	visible.SetNRGBA(1, 1, color.NRGBA{R: 124, G: 92, B: 255, A: 255})
	blank := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	cases := []struct {
		name    string
		shot    string
		wantErr bool
	}{
		{"visible", writeTestPNG(t, visible), false},
		{"blank", writeTestPNG(t, blank), true},
		{"wrong size", writeTestPNG(t, image.NewNRGBA(image.Rect(0, 0, 8, 8))), true},
		{"not a png", filepath.Join(t.TempDir(), "missing.png"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useFakeChrome(t, tc.shot)
			out := filepath.Join(t.TempDir(), "out.png")
			err := renderSVGPNG(filepath.Join(t.TempDir(), "in.svg"), out, 4, 2)
			if (err != nil) != tc.wantErr {
				t.Errorf("renderSVGPNG error = %v, want error: %v", err, tc.wantErr)
			}
		})
	}
}

// prepareRunDir makes a repository root holding the given desktop/resources
// files and a stand-in Chrome that never writes a screenshot, so a run that
// reaches rendering fails with a render error instead of an SVG error.
func prepareRunDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	resources := filepath.Join(dir, "desktop", "resources")
	if err := os.MkdirAll(resources, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(resources, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	useFakeChrome(t, filepath.Join(dir, "no-screenshot.png"))
	return resources
}

func assertRunFailsWith(t *testing.T, resources, want string, fileCount int) {
	t.Helper()
	err := run()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("run error = %v, want one containing %q", err, want)
	}
	entries, readErr := os.ReadDir(resources)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != fileCount {
		t.Errorf("run left %d files in %s, want %d", len(entries), resources, fileCount)
	}
}

func TestRunValidatesSVGBeforeRendering(t *testing.T) {
	resources := prepareRunDir(t, map[string]string{"icon.svg": `<svg viewBox="0 0 1 1"></svg>`})
	assertRunFailsWith(t, resources, "invalid SVG root", 1)
}

func TestRunFailsWithoutIconSVG(t *testing.T) {
	resources := prepareRunDir(t, map[string]string{"box-icon-app.svg": svgRoot})
	assertRunFailsWith(t, resources, "read svg", 1)
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

func TestIconMarkupDropsTitle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "icon.svg")
	src := "<svg viewBox=\"0 0 8 8\"><title>Icon</title><path d=\"M0 0\"/></svg>"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := iconMarkup(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "title") {
		t.Errorf("markup keeps the title: %q", got)
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
