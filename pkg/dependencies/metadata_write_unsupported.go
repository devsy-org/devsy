//go:build !linux && !darwin

package dependencies

import (
	"errors"
	"os"
)

func metadataWriteSupported() error {
	return errors.New(
		"atomic durable dependency metadata replacement is unsupported on this platform",
	)
}

func validatePrivateMetadata(os.FileInfo) error { return metadataWriteSupported() }

func openMetadataFile(*os.Root, string) (*os.File, error) { return nil, metadataWriteSupported() }

func syncMetadataDirectory(*os.Root) error { return metadataWriteSupported() }
