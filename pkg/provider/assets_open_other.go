//go:build !unix

package provider

import "os"

func openLocalIconFile(root *os.Root, target string) (*os.File, error) {
	return root.Open(target)
}
