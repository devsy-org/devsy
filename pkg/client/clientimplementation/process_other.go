//go:build !windows

package clientimplementation

func isInvalidProcessParameter(error) bool {
	return false
}
