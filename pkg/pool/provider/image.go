package provider

import (
	"strings"
	"unicode"
)

// CheckImage returns ErrInvalidImage for an empty reference, one that starts
// with "-" (it would read as an option), or one with whitespace or control
// characters. It does not check that the reference names a real image.
func CheckImage(image string) error {
	if image == "" || strings.HasPrefix(image, "-") ||
		strings.ContainsFunc(image, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsControl(r)
		}) {
		return ErrInvalidImage
	}
	return nil
}
