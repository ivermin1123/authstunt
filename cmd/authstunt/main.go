// Command authstunt is a self-hosted test identity manager for E2E testing.
// P0 skeleton: prints version info. Subcommands land in P1.
package main

import (
	"fmt"
	"os"
)

// Set by goreleaser at build time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("authstunt %s (commit %s, built %s)\n", version, commit, date)
		return
	}
	fmt.Println("authstunt: P0 skeleton. Run 'authstunt version'. Server lands in P1.")
}
