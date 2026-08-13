// Command authstunt is a self-hosted test identity manager for E2E testing.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

// Set by goreleaser at build time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const usage = `authstunt - a self-hosted test identity manager

Usage:
  authstunt serve [--project <name>] [--domain <pattern>]... [--data-dir <path>]
                  [--smtp-listen <addr>] [--seed-url <url>] [--pool-cooldown <duration>]
  authstunt project bearer <provision|rotate|revoke> [--data-dir <path>]
  authstunt version

serve never creates or prints a credential. The API needs a project bearer,
and "authstunt project bearer provision" is the one place one is produced.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		// A usage error has already printed what was wrong with the
		// flags, so it is not repeated here.
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(os.Stderr, "authstunt: %v\n", err)
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	switch args[0] {
	case "version":
		fmt.Printf("authstunt %s (commit %s, built %s)\n", version, commit, date)
		return nil
	case "serve":
		return runServe(args[1:], os.Stderr)
	case "project":
		return runProject(args[1:], os.Stdout, os.Stderr)
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
	}
}
