//go:build !windows

package shell

func isInvalidProcessParameter(error) bool {
	return false
}
