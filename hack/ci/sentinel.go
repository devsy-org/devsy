package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const sentinelCanary = "devsy-e2e-sentinel-canary-b03c74e8a6f1"

func sentinelCLI(args []string, stderr io.Writer) int {
	if len(args) != 2 {
		_, _ = fmt.Fprintln(stderr, "sentinel check requires sentinel and output files")
		return 2
	}
	if err := checkSentinels(args[0], args[1]); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func checkSentinels(sentinelPath, outputPath string) error {
	output, err := os.ReadFile(outputPath)
	if err != nil {
		return fmt.Errorf("cannot read captured output")
	}
	file, err := os.Open(sentinelPath)
	if err != nil {
		return fmt.Errorf("cannot read sentinel file")
	}
	defer func() { _ = file.Close() }()
	return scanSentinels(file, output)
}

func scanSentinels(file io.Reader, output []byte) error {
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	seen := make(map[string]bool)
	for index := 1; scanner.Scan(); index++ {
		var value string
		if json.Unmarshal(scanner.Bytes(), &value) != nil || value == "" {
			return fmt.Errorf("invalid sentinel at index %d", index)
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		if pos := bytes.Index(output, []byte(value)); pos >= 0 {
			return fmt.Errorf(
				"sentinel %d found at line %d",
				index,
				bytes.Count(output[:pos], []byte("\n"))+1,
			)
		}
	}
	if scanner.Err() != nil {
		return fmt.Errorf("cannot scan sentinel file")
	}
	if !seen[sentinelCanary] {
		return fmt.Errorf("no canary record: sentinel file was not populated by the e2e process")
	}
	return nil
}
