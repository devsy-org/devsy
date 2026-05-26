package up

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
)

func createTarGzArchive(outputFilePath string, filePaths []string) error {
	outputFile, err := os.Create(outputFilePath)
	if err != nil {
		return err
	}
	defer func() { _ = outputFile.Close() }()

	gzipWriter := gzip.NewWriter(outputFile)
	defer func() { _ = gzipWriter.Close() }()

	tarWriter := tar.NewWriter(gzipWriter)
	defer func() { _ = tarWriter.Close() }()

	for _, filePath := range filePaths {
		if err := addFileToTar(tarWriter, filePath); err != nil {
			return err
		}
	}
	return nil
}

func addFileToTar(tarWriter *tar.Writer, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	fileInfo, err := file.Stat()
	if err != nil {
		return err
	}

	header, err := tar.FileInfoHeader(fileInfo, fileInfo.Name())
	if err != nil {
		return err
	}

	// Normalize CRLF -> LF for shell scripts so they execute correctly inside
	// Linux containers when the test runs from a Windows host (where git may
	// have checked the file out with CRLF line endings).
	if strings.HasSuffix(strings.ToLower(filePath), ".sh") {
		content, err := io.ReadAll(file)
		if err != nil {
			return err
		}
		content = bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
		header.Size = int64(len(content))
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		_, err = tarWriter.Write(content)
		return err
	}

	if err := tarWriter.WriteHeader(header); err != nil {
		return err
	}

	_, err = io.Copy(tarWriter, file)
	return err
}

func setupDockerProvider(binDir, dockerPath string) (*framework.Framework, error) {
	return framework.SetupDockerProvider(binDir, dockerPath)
}
