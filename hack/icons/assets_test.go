package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	caseTrailingBytes = "trailing bytes"
	chunkIHDR         = "IHDR"
	chunkIDAT         = "IDAT"
	chunkIEND         = "IEND"
)

var (
	pngSignature     = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	allowedPNGChunks = []string{chunkIHDR, chunkIDAT, chunkIEND}
	allowedSVGTags   = []string{"svg", "title", "path", "g"}
	allowedSVGAttrs  = []string{
		"xmlns",
		"width",
		"height",
		"viewBox",
		"fill",
		"d",
		"transform",
		"x",
		"y",
	}
	pngChunkOverhead  = 12
	icoHeaderSize     = 6
	icoEntrySize      = 16
	icnsHeaderSize    = 8
	icnsChunkOverhead = 8
)

type pngPart struct {
	kind string
	data []byte
}

func readPNGChunk(rest []byte) (part pngPart, size int, err error) {
	if len(rest) < pngChunkOverhead {
		return pngPart{}, 0, errors.New("truncated chunk header")
	}
	length := uint64(binary.BigEndian.Uint32(rest[:4]))
	total := length + uint64(pngChunkOverhead)
	if uint64(len(rest)) < total {
		return pngPart{}, 0, errors.New("truncated chunk")
	}
	kind := string(rest[4:8])
	want := binary.BigEndian.Uint32(rest[8+length : total])
	if crc32.ChecksumIEEE(rest[4:8+length]) != want {
		return pngPart{}, 0, fmt.Errorf("chunk %q has a bad checksum", kind)
	}
	return pngPart{kind: kind, data: rest[8 : 8+length]}, int(total), nil
}

// splitPNG returns the chunks of a PNG that uses only allowed chunk types and
// has no bytes after IEND.
func splitPNG(data []byte) ([]pngPart, error) {
	if !bytes.HasPrefix(data, pngSignature) {
		return nil, errors.New("missing PNG signature")
	}
	rest := data[len(pngSignature):]
	var parts []pngPart
	for {
		part, size, err := readPNGChunk(rest)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(allowedPNGChunks, part.kind) {
			return nil, fmt.Errorf("chunk %q is not allowed", part.kind)
		}
		parts = append(parts, part)
		rest = rest[size:]
		if part.kind == chunkIEND {
			break
		}
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%d trailing bytes after IEND", len(rest))
	}
	return parts, nil
}

func checkPNGOrder(parts []pngPart) error {
	last := len(parts) - 1
	if last < 2 || parts[0].kind != chunkIHDR || parts[last].kind != chunkIEND {
		return errors.New("chunks must run IHDR, IDAT..., IEND")
	}
	for _, part := range parts[1:last] {
		if part.kind != chunkIDAT {
			return fmt.Errorf("chunk %q is out of order", part.kind)
		}
		if len(part.data) == 0 {
			return errors.New("empty IDAT chunk")
		}
	}
	return nil
}

var pngChannels = map[byte]uint64{0: 1, 2: 3, 3: 1, 4: 2, 6: 4}

// rawPNGSize is the size of the filtered scanlines an IHDR describes.
func rawPNGSize(ihdr []byte) (uint64, error) {
	const ihdrSize = 13
	if len(ihdr) != ihdrSize {
		return 0, errors.New("IHDR has the wrong size")
	}
	channels, ok := pngChannels[ihdr[9]]
	if !ok || ihdr[10] != 0 || ihdr[11] != 0 || ihdr[12] != 0 {
		return 0, errors.New("IHDR uses an unsupported colour type, method or interlace")
	}
	width := uint64(binary.BigEndian.Uint32(ihdr[0:4]))
	height := uint64(binary.BigEndian.Uint32(ihdr[4:8]))
	rowBytes := (width*channels*uint64(ihdr[8]) + 7) / 8
	return height * (1 + rowBytes), nil
}

// checkPNGPixels requires the IDAT data to be one zlib stream that inflates to
// exactly the size the IHDR implies, so a junk IDAT chunk cannot ride along.
func checkPNGPixels(parts []pngPart) error {
	want, err := rawPNGSize(parts[0].data)
	if err != nil {
		return err
	}
	var idat []byte
	for _, part := range parts[1 : len(parts)-1] {
		idat = append(idat, part.data...)
	}
	reader := bytes.NewReader(idat)
	inflater, err := zlib.NewReader(reader)
	if err != nil {
		return fmt.Errorf("IDAT is not a zlib stream: %w", err)
	}
	raw, err := io.ReadAll(inflater)
	if err != nil {
		return fmt.Errorf("IDAT does not inflate: %w", err)
	}
	if reader.Len() != 0 {
		return fmt.Errorf("%d bytes of IDAT data after the zlib stream", reader.Len())
	}
	if uint64(len(raw)) != want {
		return fmt.Errorf("IDAT inflates to %d bytes, IHDR implies %d", len(raw), want)
	}
	return nil
}

// checkPNG allows only the chunks a plain render needs and no bytes after IEND,
// so metadata such as content credentials, XMP or EXIF cannot ride along.
func checkPNG(data []byte) error {
	parts, err := splitPNG(data)
	if err != nil {
		return err
	}
	if err := checkPNGOrder(parts); err != nil {
		return err
	}
	return checkPNGPixels(parts)
}

func checkFrames(frames [][]byte) error {
	for i, frame := range frames {
		if err := checkPNG(frame); err != nil {
			return fmt.Errorf("frame %d: %w", i, err)
		}
	}
	return nil
}

func icoFrames(data []byte) ([][]byte, error) {
	if len(data) < icoHeaderSize || binary.LittleEndian.Uint16(data[2:4]) != 1 {
		return nil, errors.New("not an ICO file")
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	next := icoHeaderSize + count*icoEntrySize
	if len(data) < next {
		return nil, errors.New("truncated ICO directory")
	}
	var frames [][]byte
	for i := range count {
		entry := data[icoHeaderSize+i*icoEntrySize:]
		size := int(binary.LittleEndian.Uint32(entry[8:12]))
		offset := int(binary.LittleEndian.Uint32(entry[12:16]))
		if offset != next || offset+size > len(data) {
			return nil, fmt.Errorf("ICO entry %d is not contiguous or is truncated", i)
		}
		frames = append(frames, data[offset:offset+size])
		next = offset + size
	}
	if next != len(data) {
		return nil, fmt.Errorf("%d trailing bytes after the last ICO frame", len(data)-next)
	}
	return frames, nil
}

func icnsFrames(data []byte) ([][]byte, error) {
	if len(data) < icnsHeaderSize || string(data[:4]) != "icns" {
		return nil, errors.New("not an ICNS file")
	}
	if int(binary.BigEndian.Uint32(data[4:8])) != len(data) {
		return nil, errors.New("ICNS length does not match the file size")
	}
	var frames [][]byte
	for offset := icnsHeaderSize; offset < len(data); {
		if len(data)-offset < icnsChunkOverhead {
			return nil, errors.New("truncated ICNS chunk header")
		}
		length := int(binary.BigEndian.Uint32(data[offset+4 : offset+8]))
		if length < icnsChunkOverhead || offset+length > len(data) {
			return nil, errors.New("ICNS chunk is truncated")
		}
		frames = append(frames, data[offset+icnsChunkOverhead:offset+length])
		offset += length
	}
	return frames, nil
}

func checkICO(data []byte) error {
	frames, err := icoFrames(data)
	if err != nil {
		return err
	}
	return checkFrames(frames)
}

func checkICNS(data []byte) error {
	frames, err := icnsFrames(data)
	if err != nil {
		return err
	}
	return checkFrames(frames)
}

var svgURLRef = regexp.MustCompile(`(?i)url\(([^)]*)`)

// checkSVGValue allows url() only as a reference to an id in the same document.
func checkSVGValue(value string) error {
	if strings.Contains(value, "\\") {
		return errors.New("escapes are not allowed")
	}
	for _, match := range svgURLRef.FindAllStringSubmatch(value, -1) {
		ref := strings.Trim(strings.TrimSpace(match[1]), "'\"")
		if !strings.HasPrefix(ref, "#") || len(ref) < 2 {
			return fmt.Errorf("url(%s) is not a same-document reference", match[1])
		}
	}
	return nil
}

type svgChecker struct{ inTitle bool }

func checkSVGAttr(attr xml.Attr) error {
	if attr.Name.Space != "" || !slices.Contains(allowedSVGAttrs, attr.Name.Local) {
		return fmt.Errorf("attribute %q is not allowed", attr.Name.Local)
	}
	if attr.Name.Local == "xmlns" && attr.Value != svgNamespace {
		return fmt.Errorf("unexpected namespace %q", attr.Value)
	}
	if err := checkSVGValue(attr.Value); err != nil {
		return fmt.Errorf("attribute %q: %w", attr.Name.Local, err)
	}
	return nil
}

func (c *svgChecker) element(start xml.StartElement) error {
	if start.Name.Space != svgNamespace || !slices.Contains(allowedSVGTags, start.Name.Local) {
		return fmt.Errorf("element <%s> is not allowed", start.Name.Local)
	}
	for _, attr := range start.Attr {
		if err := checkSVGAttr(attr); err != nil {
			return err
		}
	}
	c.inTitle = start.Name.Local == "title"
	return nil
}

func (c *svgChecker) token(tok xml.Token) error {
	switch t := tok.(type) {
	case xml.StartElement:
		return c.element(t)
	case xml.EndElement:
		c.inTitle = false
	case xml.CharData:
		if !c.inTitle && strings.TrimSpace(string(t)) != "" {
			return errors.New("text outside <title>")
		}
	case xml.Comment:
		return errors.New("comments are not allowed")
	case xml.ProcInst, xml.Directive:
		return errors.New("processing instructions and directives are not allowed")
	}
	return nil
}

func checkSVG(data []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	checker := &svgChecker{}
	for {
		tok, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := checker.token(tok); err != nil {
			return err
		}
	}
}

func shippedPNGs(t *testing.T) []string {
	t.Helper()
	paths := []string{repoFile("desktop", "resources", "icon.png")}
	icons, err := filepath.Glob(repoFile("desktop", "resources", "icons", "*.png"))
	if err != nil {
		t.Fatal(err)
	}
	media := repoFile("sites", "docs-devsy-sh", "public", "docs", "media")
	for _, name := range []string{"devsy.png", "devsy-dark.png", "devsy-icon-128.png"} {
		paths = append(paths, filepath.Join(media, name))
	}
	return append(paths, icons...)
}

func shippedSVGs() []string {
	media := repoFile("sites", "docs-devsy-sh", "public", "docs", "media")
	return []string{
		repoFile("desktop", "resources", "icon.svg"),
		repoFile("desktop", "resources", "box-icon-app.svg"),
		repoFile("sites", "docs-devsy-sh", "app", "icon.svg"),
		filepath.Join(media, "devsy-favicon.svg"),
		filepath.Join(media, "devsy-logo-horizontal.svg"),
		filepath.Join(media, "devsy-logo-horizontal-dark.svg"),
	}
}

func checkFile(t *testing.T, path string, check func([]byte) error) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := check(data); err != nil {
		t.Errorf("%s: %v", path, err)
	}
}

func TestShippedAssetsCarryNoMetadata(t *testing.T) {
	for _, path := range shippedPNGs(t) {
		checkFile(t, path, checkPNG)
	}
	checkFile(t, repoFile("desktop", "resources", "icon.ico"), checkICO)
	checkFile(t, repoFile("desktop", "resources", "icon.icns"), checkICNS)
	for _, path := range shippedSVGs() {
		checkFile(t, path, checkSVG)
	}
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func pngChunk(kind string, body []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
	out = append(out, kind...)
	out = append(out, body...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte(kind), body...)))
}

func withChunkBeforeIEND(data []byte, chunk []byte) []byte {
	iend := len(data) - pngChunkOverhead
	return slices.Concat(data[:iend], chunk, data[iend:])
}

func TestPNGAllowlistRejectsMetadata(t *testing.T) {
	base := tinyPNG(t)
	if err := checkPNG(base); err != nil {
		t.Fatalf("plain PNG rejected: %v", err)
	}
	xmp := append([]byte("XML:com.adobe.xmp\x00\x00\x00\x00\x00"), "<x:xmpmeta/>"...)
	cases := map[string][]byte{
		"caBX content credentials": withChunkBeforeIEND(
			base,
			pngChunk("caBX", []byte("jumb c2pa")),
		),
		"iTXt XMP": withChunkBeforeIEND(base, pngChunk("iTXt", xmp)),
		"tEXt": withChunkBeforeIEND(
			base,
			pngChunk("tEXt", []byte("Software\x00tool")),
		),
		"eXIf":            withChunkBeforeIEND(base, pngChunk("eXIf", []byte("Exif"))),
		caseTrailingBytes: append(slices.Clone(base), 0x00),
		"trailing PNG chunk": append(
			slices.Clone(base),
			pngChunk("tEXt", []byte("a\x00b"))...),
		"bad checksum": append(
			slices.Clone(base[:len(base)-1]),
			base[len(base)-1]^0xff,
		),
		"truncated": base[:len(base)-4],
	}
	for name, data := range cases {
		if err := checkPNG(data); err == nil {
			t.Errorf("%s: expected PNG to be rejected", name)
		}
	}
}

func buildPNG(parts []pngPart) []byte {
	out := slices.Clone(pngSignature)
	for _, part := range parts {
		out = append(out, pngChunk(part.kind, part.data)...)
	}
	return out
}

func zlibBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zlib.NewWriter(&buf)
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPNGAllowlistRejectsPixelTricks(t *testing.T) {
	parts, err := splitPNG(tinyPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	ihdr, idat, iend := parts[0], parts[1], parts[len(parts)-1]
	if err := checkPNG(buildPNG(parts)); err != nil {
		t.Fatalf("rebuilt PNG rejected: %v", err)
	}
	junk := pngPart{kind: chunkIDAT, data: []byte("not pixels, just a payload")}
	exact := pngPart{kind: chunkIDAT, data: zlibBytes(t, make([]byte, 18))}
	taller := pngPart{kind: chunkIHDR, data: slices.Clone(ihdr.data)}
	binary.BigEndian.PutUint32(taller.data[4:8], 3)
	interlaced := pngPart{kind: chunkIHDR, data: slices.Clone(ihdr.data)}
	interlaced.data[12] = 1
	expectRejected(t, checkPNG, map[string][]byte{
		"junk IDAT after the pixels":  buildPNG([]pngPart{ihdr, idat, junk, iend}),
		"junk IDAT before the pixels": buildPNG([]pngPart{ihdr, junk, idat, iend}),
		"second zlib stream":          buildPNG([]pngPart{ihdr, idat, exact, iend}),
		"pixels too short": buildPNG(
			[]pngPart{ihdr, {kind: chunkIDAT, data: zlibBytes(t, make([]byte, 17))}, iend},
		),
		"pixels too long": buildPNG(
			[]pngPart{ihdr, {kind: chunkIDAT, data: zlibBytes(t, make([]byte, 19))}, iend},
		),
		"IHDR claims a taller image": buildPNG([]pngPart{taller, idat, iend}),
		"interlaced":                 buildPNG([]pngPart{interlaced, idat, iend}),
		"IDAT before IHDR":           buildPNG([]pngPart{idat, ihdr, iend}),
		"empty IDAT":                 buildPNG([]pngPart{ihdr, idat, {kind: chunkIDAT}, iend}),
		"no IDAT":                    buildPNG([]pngPart{ihdr, iend}),
		"second IHDR":                buildPNG([]pngPart{ihdr, ihdr, idat, iend}),
	})
}

func TestSVGAllowlistRejectsExternalURLs(t *testing.T) {
	const open = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 8 8">`
	for _, fill := range []string{"url(#a)", "url('#a')", `url( '#a' )`} {
		src := open + `<path fill="` + fill + `" d="M0 0"/></svg>`
		if err := checkSVG([]byte(src)); err != nil {
			t.Errorf("%s: same-document reference rejected: %v", fill, err)
		}
	}
	for _, fill := range []string{
		"url(https://example.com/a.svg#x)",
		"url('https://example.com/a.svg')",
		"url(//example.com/a.svg#x)",
		"url(data:image/svg+xml;base64,AAAA)",
		"url(a.svg#x)",
		"URL(https://example.com/a.svg)",
		"url(#)",
		"url()",
		`u\72l(https://example.com/a.svg)`,
		"#fff url(https://example.com/a.svg)",
	} {
		src := open + `<path fill="` + fill + `" d="M0 0"/></svg>`
		if err := checkSVG([]byte(src)); err == nil {
			t.Errorf("%s: expected the fill to be rejected", fill)
		}
	}
}

func packedIcons(t *testing.T) (frame, ico, icns []byte) {
	t.Helper()
	frame = tinyPNG(t)
	icoIn := map[int][]byte{}
	for _, size := range icoSizes {
		icoIn[size] = frame
	}
	icnsIn := map[int][]byte{}
	for _, entry := range icnsTags {
		icnsIn[entry.size] = frame
	}
	ico, err := packICO(icoIn)
	if err != nil {
		t.Fatal(err)
	}
	icns, err = packICNS(icnsIn)
	if err != nil {
		t.Fatal(err)
	}
	return frame, ico, icns
}

func expectRejected(t *testing.T, check func([]byte) error, cases map[string][]byte) {
	t.Helper()
	for name, data := range cases {
		if err := check(data); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

func TestICOAllowlistRejectsMetadata(t *testing.T) {
	frame, ico, _ := packedIcons(t)
	if err := checkICO(ico); err != nil {
		t.Fatalf("plain ICO rejected: %v", err)
	}
	expectRejected(t, checkICO, map[string][]byte{
		caseTrailingBytes: append(slices.Clone(ico), 0x00),
		"trailing PNG":    append(slices.Clone(ico), frame...),
		"truncated":       ico[:len(ico)-3],
		"frame with caBX": icoWithFrame(
			t,
			ico,
			withChunkBeforeIEND(frame, pngChunk("caBX", []byte("jumb"))),
		),
		"frame with trailing": icoWithFrame(t, ico, append(slices.Clone(frame), 0x00)),
	})
}

func TestICNSAllowlistRejectsMetadata(t *testing.T) {
	frame, _, icns := packedIcons(t)
	if err := checkICNS(icns); err != nil {
		t.Fatalf("plain ICNS rejected: %v", err)
	}
	grown := append(slices.Clone(icns), 0x00)
	binary.BigEndian.PutUint32(grown[4:8], uint32(len(grown)))
	text := withChunkBeforeIEND(frame, pngChunk("tEXt", []byte("a\x00b")))
	expectRejected(t, checkICNS, map[string][]byte{
		caseTrailingBytes:     grown,
		"length mismatch":     append(slices.Clone(icns), 0x00),
		"frame with tEXt":     icnsWithFrame(t, icns, text),
		"frame with trailing": icnsWithFrame(t, icns, append(slices.Clone(frame), 0x00)),
	})
}

func icnsWithFrame(t *testing.T, icns, frame []byte) []byte {
	t.Helper()
	out := slices.Concat(icns[:icnsHeaderSize], []byte(icns[icnsHeaderSize:icnsHeaderSize+4]),
		binary.BigEndian.AppendUint32(nil, uint32(icnsChunkOverhead+len(frame))), frame)
	binary.BigEndian.PutUint32(out[4:8], uint32(len(out)))
	return out
}

func icoWithFrame(t *testing.T, ico, frame []byte) []byte {
	t.Helper()
	count := int(binary.LittleEndian.Uint16(ico[4:6]))
	dir := slices.Clone(ico[:icoHeaderSize+count*icoEntrySize])
	body := slices.Clone(frame)
	for i := range count {
		entry := dir[icoHeaderSize+i*icoEntrySize:]
		binary.LittleEndian.PutUint32(entry[8:12], uint32(len(frame)))
		binary.LittleEndian.PutUint32(entry[12:16], uint32(len(dir)+i*len(frame)))
	}
	for range count - 1 {
		body = append(body, frame...)
	}
	return slices.Concat(dir, body)
}

func TestSVGAllowlistRejectsMetadata(t *testing.T) {
	const open = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 8 8" width="8" height="8">`
	plain := open + `<title>Icon</title><g fill="#fff"><path d="M0 0"/></g></svg>`
	if err := checkSVG([]byte(plain)); err != nil {
		t.Fatalf("plain SVG rejected: %v", err)
	}
	cases := map[string]string{
		"metadata":      open + `<metadata>rdf</metadata><path d="M0 0"/></svg>`,
		"comment":       open + `<!-- made by a tool --><path d="M0 0"/></svg>`,
		"desc":          open + `<desc>generated</desc><path d="M0 0"/></svg>`,
		"foreignObject": open + `<foreignObject><p/></foreignObject></svg>`,
		"image":         open + `<image width="1" height="1"/></svg>`,
		"external href": open + `<use href="https://example.com/a.svg"/></svg>`,
		"xlink href":    open + `<path xmlns:xlink="http://www.w3.org/1999/xlink" xlink:href="a.svg" d="M0 0"/></svg>`,
		"script":        open + `<script>alert(1)</script></svg>`,
		"style":         open + `<style>*{fill:red}</style></svg>`,
		"stray text":    open + `hello<path d="M0 0"/></svg>`,
		"data attr":     open + `<path data-origin="tool" d="M0 0"/></svg>`,
		"doctype":       `<!DOCTYPE svg SYSTEM "x.dtd">` + plain,
		"xml prolog":    `<?xml version="1.0"?>` + plain,
		"wrong ns":      `<svg xmlns="http://example.com/ns" viewBox="0 0 8 8"></svg>`,
	}
	for name, src := range cases {
		if err := checkSVG([]byte(src)); err == nil {
			t.Errorf("%s: expected SVG to be rejected", name)
		}
	}
}

func TestChromeLookupHonorsEnvOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chrome")
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(chromeEnv, path)
	got, err := findChromeBinary()
	if err != nil || got != path {
		t.Fatalf("got %q, %v; want %q", got, err, path)
	}
	t.Setenv(chromeEnv, filepath.Join(t.TempDir(), "missing"))
	if _, err := findChromeBinary(); err == nil {
		t.Error("a missing override must be an error, not fall back silently")
	}
}

func TestPlaywrightChromiumFindsAnyRevision(t *testing.T) {
	home := t.TempDir()
	want := filepath.Join(
		home,
		".cache",
		"ms-playwright",
		"chromium-9999",
		"chrome-linux64",
		"chrome",
	)
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := playwrightChromium(home); !slices.Equal(got, []string{want}) {
		t.Errorf("got %v, want [%s]", got, want)
	}
}
