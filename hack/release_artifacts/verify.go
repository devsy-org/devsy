package main

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
)

func verifyCmd() *cli.Command {
	return &cli.Command{
		Name:  "verify",
		Usage: "assert that the binary at -file targets -goos / -goarch",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "file", Usage: "binary to verify", Required: true},
			&cli.StringFlag{Name: "goos", Usage: "expected GOOS", Required: true},
			&cli.StringFlag{Name: "goarch", Usage: "expected GOARCH", Required: true},
		},
		Action: func(_ context.Context, c *cli.Command) error {
			want := Arch{GOOS: c.String("goos"), GOARCH: c.String("goarch")}
			return verify(c.String("file"), want)
		},
	}
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
