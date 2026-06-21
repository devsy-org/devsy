package main

import (
	"errors"
	"flag"
	"fmt"
)

func cmdVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	file := fs.String("file", "", "binary to verify")
	goos := fs.String("goos", "", "expected GOOS")
	goarch := fs.String("goarch", "", "expected GOARCH")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" || *goos == "" || *goarch == "" {
		return errors.New("verify: missing required flag (-file, -goos, -goarch)")
	}

	got, err := FromFile(*file)
	if err != nil {
		return fmt.Errorf("::error::%s: %w", *file, err)
	}
	want := Arch{GOOS: *goos, GOARCH: *goarch}
	fmt.Printf("file=%s detected=%s expected=%s\n", *file, got, want)
	if got != want {
		return fmt.Errorf("::error::arch mismatch: %s is %s, expected %s", *file, got, want)
	}
	return nil
}
