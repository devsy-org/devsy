// verify_binary_arch asserts that the binary at -file targets the architecture
// at -goos / -goarch. Fails the process with exit 1 on mismatch so the calling
// CI step turns red.
//
// Usage: verify_binary_arch -file <path> -goos <goos> -goarch <goarch>
package main

import (
	"flag"
	"fmt"
	"os"

	binaryarch "github.com/devsy-org/devsy/hack/binary_arch"
)

func main() {
	file := flag.String("file", "", "binary to verify")
	goos := flag.String("goos", "", "expected GOOS")
	goarch := flag.String("goarch", "", "expected GOARCH")
	flag.Parse()
	if *file == "" || *goos == "" || *goarch == "" {
		fmt.Fprintln(os.Stderr, "missing required flag (-file, -goos, -goarch)")
		os.Exit(2)
	}

	got, err := binaryarch.FromFile(*file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "::error::%s: %v\n", *file, err)
		os.Exit(1)
	}
	want := binaryarch.Arch{GOOS: *goos, GOARCH: *goarch}
	fmt.Printf("file=%s detected=%s expected=%s\n", *file, got, want)
	if got != want {
		fmt.Fprintf(os.Stderr, "::error::arch mismatch: %s is %s, expected %s\n",
			*file, got, want)
		os.Exit(1)
	}
}
