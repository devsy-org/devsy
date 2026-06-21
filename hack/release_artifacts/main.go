// release_artifacts: subcommands for inspecting CLI binaries staged by the
// release pipeline. See cmdInventory and cmdVerify.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "inventory":
		err = cmdInventory(os.Args[2:])
	case "stage":
		err = cmdStage(os.Args[2:])
	case "verify":
		err = cmdVerify(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: release_artifacts {inventory|stage|verify} [flags]")
}
