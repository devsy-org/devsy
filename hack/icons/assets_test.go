package main

import (
	"bytes"
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
	"slices"
	"strings"
	"testing"
)

const caseTrailingBytes = "trailing bytes"

var (
	pngSignature     = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	allowedPNGChunks = []string{"IHDR", "IDAT", "IEND"}
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
	svgNamespace      = "http://www.w3.org/2000/svg"
	pngChunkOverhead  = 12
	icoHeaderSize     = 6
	icoEntrySize      = 16
	icnsHeaderSize    = 8
	icnsChunkOverhead = 8
)

func readPNGChunk(rest []byte) (kind string, size int, err error) {
	if len(rest) < pngChunkOverhead {
		return "", 0, errors.New("truncated chunk header")
	}
	length := uint64(binary.BigEndian.Uint32(rest[:4]))
	total := length + uint64(pngChunkOverhead)
	if uint64(len(rest)) < total {
		return "", 0, errors.New("truncated chunk")
	}
	kind = string(rest[4:8])
	want := binary.BigEndian.Uint32(rest[8+length : total])
	if crc32.ChecksumIEEE(rest[4:8+length]) != want {
		return "", 0, fmt.Errorf("chunk %q has a bad checksum", kind)
	}
	return kind, int(total), nil
}

// checkPNG allows only the chunks a plain render needs and no bytes after IEND,
// so metadata such as content credentials, XMP or EXIF cannot ride along.
func checkPNG(data []byte) error {
	if !bytes.HasPrefix(data, pngSignature) {
		return errors.New("missing PNG signature")
	}
	rest := data[len(pngSignature):]
	for {
		kind, size, err := readPNGChunk(rest)
		if err != nil {
			return err
		}
		if !slices.Contains(allowedPNGChunks, kind) {
			return fmt.Errorf("chunk %q is not allowed", kind)
		}
		rest = rest[size:]
		if kind == "IEND" {
			break
		}
	}
	if len(rest) != 0 {
		return fmt.Errorf("%d trailing bytes after IEND", len(rest))
	}
	return nil
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

type svgChecker struct{ inTitle bool }

func (c *svgChecker) element(start xml.StartElement) error {
	if start.Name.Space != svgNamespace || !slices.Contains(allowedSVGTags, start.Name.Local) {
		return fmt.Errorf("element <%s> is not allowed", start.Name.Local)
	}
	for _, attr := range start.Attr {
		if attr.Name.Space != "" || !slices.Contains(allowedSVGAttrs, attr.Name.Local) {
			return fmt.Errorf("attribute %q is not allowed", attr.Name.Local)
		}
		if attr.Name.Local == "xmlns" && attr.Value != svgNamespace {
			return fmt.Errorf("unexpected namespace %q", attr.Value)
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
