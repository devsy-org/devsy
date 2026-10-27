package provider

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"strings"

	"golang.org/x/image/riff"
	"golang.org/x/image/vp8"
	"golang.org/x/image/vp8l"
	"golang.org/x/image/webp"
)

const (
	maxIconBytes     = 1 << 20
	maxIconDimension = 4096

	mimePNG  = "image/png"
	mimeJPEG = "image/jpeg"
	mimeWebP = "image/webp"
	mimeSVG  = "image/svg+xml"

	svgNamespace   = "http://www.w3.org/2000/svg"
	xhtmlNamespace = "http://www.w3.org/1999/xhtml"
)

var (
	pngSignature = []byte("\x89PNG\r\n\x1a\n")
	jpegPrefix   = []byte{0xFF, 0xD8, 0xFF}
	utf8BOM      = []byte{0xEF, 0xBB, 0xBF}

	// activeSVGElements can run code or pull in external or embedded documents.
	activeSVGElements = map[string]bool{
		"script": true, "foreignobject": true, "iframe": true, "object": true,
		"embed": true, "image": true, "feimage": true, "audio": true, "video": true,
		"canvas": true, "link": true, "meta": true, "handler": true, "listener": true,
	}
)

func isAllowedIconMime(mimeType string) bool {
	switch mimeType {
	case mimePNG, mimeJPEG, mimeWebP, mimeSVG:
		return true
	default:
		return false
	}
}

// validateIconImage checks content, not names or declared types, and returns
// the detected MIME type. declaredContentType only rejects HTML error pages.
func validateIconImage(data []byte, declaredContentType string) (string, error) {
	if len(data) == 0 {
		return "", errors.New("image is empty")
	}
	if len(data) > maxIconBytes {
		return "", fmt.Errorf("image exceeds %d bytes", maxIconBytes)
	}
	if isHTMLContentType(declaredContentType) {
		return "", errors.New("response is an HTML document, not an image")
	}

	return validateIconFormat(data)
}

type rasterImageDecoder struct {
	format        string
	mimeType      string
	decodeConfig  func(io.Reader) (image.Config, error)
	validateFrame func([]byte, image.Config) error
	decode        func(io.Reader) (image.Image, error)
}

func validateIconFormat(data []byte) (string, error) {
	switch {
	case bytes.HasPrefix(data, pngSignature):
		return validateRasterIcon(data, rasterImageDecoder{
			format: "PNG", mimeType: mimePNG, decodeConfig: png.DecodeConfig, decode: png.Decode,
		})
	case bytes.HasPrefix(data, jpegPrefix):
		return validateRasterIcon(data, rasterImageDecoder{
			format:       "JPEG",
			mimeType:     mimeJPEG,
			decodeConfig: jpeg.DecodeConfig,
			decode:       jpeg.Decode,
		})
	case isRIFFWebP(data):
		return validateRasterIcon(data, rasterImageDecoder{
			format:        "WebP",
			mimeType:      mimeWebP,
			decodeConfig:  webp.DecodeConfig,
			validateFrame: validateWebPFrame,
			decode:        webp.Decode,
		})
	}

	if err := validateSVG(data); err != nil {
		return "", err
	}
	return mimeSVG, nil
}

func validateRasterIcon(data []byte, decoder rasterImageDecoder) (string, error) {
	cfg, err := decoder.decodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("invalid %s: %w", decoder.format, err)
	}
	if err := checkDimensions(cfg.Width, cfg.Height); err != nil {
		return "", err
	}
	if err := validateRasterHeader(data, cfg, decoder.validateFrame); err != nil {
		return "", err
	}
	decoded, err := decoder.decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("invalid %s: %w", decoder.format, err)
	}
	if err := validateRasterOutput(decoded, cfg, decoder.validateFrame != nil); err != nil {
		return "", err
	}
	return decoder.mimeType, nil
}

func validateRasterHeader(
	data []byte,
	cfg image.Config,
	validateFrame func([]byte, image.Config) error,
) error {
	if validateFrame == nil {
		return nil
	}
	return validateFrame(data, cfg)
}

func validateRasterOutput(decoded image.Image, cfg image.Config, checkCanvas bool) error {
	if !checkCanvas {
		return nil
	}
	bounds := decoded.Bounds()
	if err := checkDimensions(bounds.Dx(), bounds.Dy()); err != nil {
		return err
	}
	if bounds.Dx() != cfg.Width || bounds.Dy() != cfg.Height {
		return errors.New("decoded WebP dimensions do not match its header")
	}
	return nil
}

func isHTMLContentType(declared string) bool {
	mediaType, _, err := mime.ParseMediaType(declared)
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(strings.Split(declared, ";")[0]))
	}
	return mediaType == "text/html" || mediaType == "application/xhtml+xml"
}

func checkDimensions(width, height int) error {
	if width < 1 || height < 1 {
		return errors.New("image has no pixels")
	}
	if width > maxIconDimension || height > maxIconDimension {
		return fmt.Errorf("image is %dx%d, larger than %d pixels per side",
			width, height, maxIconDimension)
	}
	return nil
}

func validateWebPFrame(data []byte, canvas image.Config) error {
	form, chunks, err := riff.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("invalid WebP: %w", err)
	}
	if form != (riff.FourCC{'W', 'E', 'B', 'P'}) {
		return errors.New("invalid WebP form")
	}
	return validateWebPChunks(chunks, canvas, len(data))
}

func validateWebPChunks(chunks *riff.Reader, canvas image.Config, encodedLength int) error {
	for {
		chunkID, chunkLength, chunkData, err := chunks.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("invalid WebP: image frame is missing")
		}
		if err != nil {
			return fmt.Errorf("invalid WebP: %w", err)
		}
		if chunkID == (riff.FourCC{'V', 'P', '8', ' '}) &&
			int64(chunkLength) > int64(encodedLength) {
			return errors.New("invalid WebP: frame chunk exceeds encoded body")
		}
		frame, found, err := decodeWebPFrameChunk(chunkID, chunkLength, chunkData)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		return validateWebPCanvas(frame, canvas)
	}
}

func decodeWebPFrameChunk(
	chunkID riff.FourCC,
	chunkLength uint32,
	chunkData io.Reader,
) (image.Config, bool, error) {
	switch chunkID {
	case riff.FourCC{'V', 'P', '8', ' '}:
		decoder := vp8.NewDecoder()
		decoder.Init(chunkData, int(chunkLength))
		header, err := decoder.DecodeFrameHeader()
		if err != nil {
			return image.Config{}, false, fmt.Errorf("invalid WebP frame: %w", err)
		}
		return image.Config{Width: header.Width, Height: header.Height}, true, nil
	case riff.FourCC{'V', 'P', '8', 'L'}:
		frame, err := vp8l.DecodeConfig(chunkData)
		if err != nil {
			return image.Config{}, false, fmt.Errorf("invalid WebP frame: %w", err)
		}
		return frame, true, nil
	default:
		return image.Config{}, false, nil
	}
}

func validateWebPCanvas(frame, canvas image.Config) error {
	if err := checkDimensions(frame.Width, frame.Height); err != nil {
		return err
	}
	if frame.Width != canvas.Width || frame.Height != canvas.Height {
		return fmt.Errorf(
			"WebP frame dimensions %dx%d do not match canvas %dx%d",
			frame.Width,
			frame.Height,
			canvas.Width,
			canvas.Height,
		)
	}
	return nil
}

func isRIFFWebP(data []byte) bool {
	return len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP"
}

// validateSVG requires a well-formed SVG document without DTDs or entity
// declarations, active or externally embedding elements, event handlers and
// non-fragment references. Shapes, paths, gradients, internal fragment
// references and simple stylesheets stay allowed.
func validateSVG(data []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, utf8BOM)))
	state := &svgValidator{}

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid SVG: not well-formed XML: %w", err)
		}
		if err := state.check(token); err != nil {
			return fmt.Errorf("invalid SVG: %w", err)
		}
	}

	if !state.sawRoot {
		return errors.New("unsupported image: content is not PNG, JPEG, WebP or SVG")
	}
	return nil
}

type svgValidator struct {
	depth     int
	sawRoot   bool
	inStyle   bool
	styleText strings.Builder
}

func (v *svgValidator) check(token xml.Token) error {
	switch t := token.(type) {
	case xml.Directive:
		return errors.New("DTD and entity declarations are not allowed")
	case xml.ProcInst:
		if t.Target != "xml" {
			return fmt.Errorf("processing instruction %q is not allowed", t.Target)
		}
	case xml.CharData:
		return v.checkCharData(t)
	case xml.StartElement:
		return v.start(t)
	case xml.EndElement:
		return v.end()
	}
	return nil
}

func (v *svgValidator) end() error {
	v.depth--
	if !v.inStyle {
		return nil
	}
	if err := checkCSS(v.styleText.String()); err != nil {
		return err
	}
	v.styleText.Reset()
	v.inStyle = false
	return nil
}

func (v *svgValidator) checkCharData(data xml.CharData) error {
	if v.depth == 0 && len(bytes.TrimSpace(data)) > 0 {
		return errors.New("text outside the root element")
	}
	if v.inStyle {
		_, _ = v.styleText.Write(data)
	}
	return nil
}

func (v *svgValidator) start(element xml.StartElement) error {
	if v.inStyle {
		return errors.New("elements inside stylesheets are not allowed")
	}
	if err := v.checkRoot(element.Name); err != nil {
		return err
	}
	v.depth++

	local := strings.ToLower(element.Name.Local)
	if activeSVGElements[local] || element.Name.Space == xhtmlNamespace {
		return fmt.Errorf("element %q is not allowed", element.Name.Local)
	}
	if local == "style" {
		v.inStyle = true
	}

	for _, attr := range element.Attr {
		if err := checkSVGAttribute(local, attr); err != nil {
			return err
		}
	}
	return nil
}

func (v *svgValidator) checkRoot(name xml.Name) error {
	if !v.sawRoot {
		if name.Local != "svg" || name.Space != svgNamespace {
			return errors.New("root element is not an svg in the SVG namespace")
		}
		v.sawRoot = true
	} else if v.depth == 0 {
		return errors.New("multiple root elements")
	}
	return nil
}

func checkSVGAttribute(element string, attr xml.Attr) error {
	name := strings.ToLower(attr.Name.Local)
	value := strings.TrimSpace(attr.Value)
	if attr.Name.Space == "http://www.w3.org/XML/1998/namespace" && name == "base" {
		return errors.New("xml:base is not allowed")
	}

	if strings.HasPrefix(name, "on") {
		return fmt.Errorf("event handler attribute %q is not allowed", attr.Name.Local)
	}
	if name == "href" && !strings.HasPrefix(value, "#") {
		return errors.New("only internal fragment references are allowed")
	}
	if isReferenceAnimation(element, name, value) {
		return errors.New("animating references is not allowed")
	}

	return checkCSS(value)
}

func isReferenceAnimation(element, name, value string) bool {
	return name == "attributename" && isAnimationElement(element) &&
		strings.HasSuffix(strings.ToLower(value), "href")
}

func isAnimationElement(element string) bool {
	switch element {
	case "animate", "set", "animatetransform", "animatemotion":
		return true
	default:
		return false
	}
}

// checkCSS rejects script schemes, imports, escapes and any url() that is not
// an internal fragment. Values without those markers pass unchanged.
func checkCSS(value string) error {
	lower := strings.ToLower(value)
	for _, banned := range []string{"javascript:", "vbscript:", "expression(", "@import"} {
		if strings.Contains(lower, banned) {
			return fmt.Errorf("%q is not allowed", banned)
		}
	}
	if strings.ContainsRune(value, '\\') {
		return errors.New("escaped CSS references are not allowed")
	}

	for rest := lower; ; {
		index := strings.Index(rest, "url(")
		if index < 0 {
			return nil
		}
		rest = strings.TrimLeft(rest[index+len("url("):], " \t\r\n'\"")
		if !strings.HasPrefix(rest, "#") {
			return errors.New("only internal url(#fragment) references are allowed")
		}
	}
}
