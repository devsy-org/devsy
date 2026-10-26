package provider

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"strings"

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

func validateIconFormat(data []byte) (string, error) {
	switch {
	case bytes.HasPrefix(data, pngSignature):
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return "", fmt.Errorf("invalid PNG: %w", err)
		}
		return mimePNG, checkDimensions(cfg.Width, cfg.Height)
	case bytes.HasPrefix(data, jpegPrefix):
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return "", fmt.Errorf("invalid JPEG: %w", err)
		}
		return mimeJPEG, checkDimensions(cfg.Width, cfg.Height)
	case isRIFFWebP(data):
		cfg, err := webp.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return "", fmt.Errorf("invalid WebP: %w", err)
		}
		return mimeWebP, checkDimensions(cfg.Width, cfg.Height)
	}

	if err := validateSVG(data); err != nil {
		return "", err
	}
	return mimeSVG, nil
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
