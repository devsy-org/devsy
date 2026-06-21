package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func verifyCmd() *cobra.Command {
	var file, goos, goarch string
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "assert that the binary at --file targets --goos / --goarch",
		RunE: func(_ *cobra.Command, _ []string) error {
			return verify(file, Arch{GOOS: goos, GOARCH: goarch})
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "binary to verify")
	cmd.Flags().StringVar(&goos, "goos", "", "expected GOOS")
	cmd.Flags().StringVar(&goarch, "goarch", "", "expected GOARCH")
	for _, f := range []string{"file", "goos", "goarch"} {
		_ = cmd.MarkFlagRequired(f)
	}
	return cmd
}

func verify(file string, want Arch) error {
	got, err := FromFile(file)
	if err != nil {
		return fmt.Errorf("::error::%s: %w", file, err)
	}
	fmt.Printf("file=%s detected=%s expected=%s\n", file, got, want)
	if got != want {
		return fmt.Errorf("::error::arch mismatch: %s is %s, expected %s", file, got, want)
	}
	return nil
}
