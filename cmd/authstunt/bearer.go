package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/ivermin1123/authstunt/internal/ledger"
	"github.com/ivermin1123/authstunt/internal/store"
)

// projectUsage is printed for `authstunt project` with no operation.
const projectUsage = `authstunt project bearer - manage the project's API credential

Usage:
  authstunt project bearer provision [--data-dir <path>] [--project <name>] [--domain <pattern>]...
  authstunt project bearer rotate    [--data-dir <path>] [--project <name>]
  authstunt project bearer revoke    [--data-dir <path>] [--project <name>]

provision mints the credential the HTTP API authenticates with, rotate
replaces it and cuts off the previous value, and revoke removes it and
leaves the API unable to authenticate anybody.

The raw value is shown once, on stdout, and is never recoverable
afterwards: it is not written into the data directory, not logged, and not
present in evidence. Store it in a secret manager from the terminal that
printed it.

By default the value is only shown on a terminal. A pipe, a file, or a CI
job is refused, because the value would land in whatever collects that
output. --allow-non-tty-reveal overrides the refusal and makes the caller
responsible for where the value goes.
`

// bearerOptions is the flag set the three operations share.
type bearerOptions struct {
	dataDir string
	project string
	domains domainList
	// allowNonTTY is the operator taking responsibility for a sink this
	// program cannot inspect.
	allowNonTTY bool
}

// runProject is the `project` subcommand tree.
//
// It exists as its own command rather than as a serve flag because a
// credential must only ever be produced by an action somebody typed. A
// flag on the long-running process would put the value into that process's
// output, which is exactly the sink - CI log, journal, container runtime,
// log shipper - the value has to stay out of.
func runProject(args []string, stdout, stderr io.Writer) error {
	if len(args) < 2 || args[0] != "bearer" {
		note(stderr, projectUsage)
		return errors.New("project: expected `project bearer <provision|rotate|revoke>`")
	}
	operation := args[1]
	switch operation {
	case "provision", "rotate", "revoke":
	default:
		note(stderr, projectUsage)
		return fmt.Errorf("project bearer: unknown operation %q", operation)
	}

	var opts bearerOptions
	fs := flag.NewFlagSet("project bearer "+operation, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.dataDir, "data-dir", "", "data directory (default ~/.authstunt/<project>)")
	fs.StringVar(&opts.project, "project", "", "project name (required on an uninitialized data dir)")
	fs.Var(&opts.domains, "domain",
		"allowlisted domain pattern, repeatable; required to initialize an empty data dir")
	fs.BoolVar(&opts.allowNonTTY, "allow-non-tty-reveal", false,
		"print the credential even when the output is not a terminal; the caller owns wherever it lands")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}

	dataDir, err := resolveDataDir(serveOptions{project: opts.project, dataDir: opts.dataDir})
	if err != nil {
		return fmt.Errorf("project bearer: %s", strings.TrimPrefix(err.Error(), "serve: "))
	}
	return runBearerOperation(context.Background(), operation, opts, dataDir, stdout, stderr)
}

// runBearerOperation opens the data directory and applies one operation.
func runBearerOperation(ctx context.Context, operation string, opts bearerOptions,
	dataDir string, stdout, stderr io.Writer) error {
	// The logger goes to stderr, and it never sees a credential: the value
	// is written straight to stdout below, so no handler, formatter or
	// future log destination is ever handed it.
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	st, err := openDataDir(ctx, dataDir, logger)
	if err != nil {
		return fmt.Errorf("project bearer: %w", err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			logger.Error("project bearer: closing the store failed", "error", err)
		}
	}()

	project, _, err := bootstrap(ctx, st, opts.project, opts.domains)
	if err != nil {
		return fmt.Errorf("project bearer: %w", err)
	}
	has, err := st.ProjectHasBearer(ctx, project.ID)
	if err != nil {
		return fmt.Errorf("project bearer: %w", err)
	}

	switch operation {
	case "revoke":
		return revokeBearer(ctx, st, project, has, stderr)
	case "provision":
		if has {
			return errAlreadyProvisioned
		}
		return mintBearer(ctx, st, project, ledger.BearerProvisioned, opts, stdout, stderr)
	default: // rotate
		if !has {
			return errNothingToRotate
		}
		return mintBearer(ctx, st, project, ledger.BearerRotated, opts, stdout, stderr)
	}
}

// errAlreadyProvisioned refuses to overwrite a live credential from the
// operation whose name does not say it would.
//
// Making provision idempotent-by-overwrite would mean a re-run silently
// cut off every consumer of the current value. Rotation is a decision, so
// it has its own word.
var errAlreadyProvisioned = errors.New(
	"project bearer: this project already has a bearer; " +
		"`authstunt project bearer rotate` replaces it and cuts off the current value")

// errNothingToRotate refuses to treat rotation as provisioning.
//
// The two are told apart for the same reason: an operator who typed rotate
// believes a value exists somewhere and expects the old one to stop
// working. Being told there was never one is the useful answer.
var errNothingToRotate = errors.New(
	"project bearer: this project has no bearer to rotate; " +
		"`authstunt project bearer provision` creates the first one")

// errRevealRefused stops a credential from being written to a sink this
// program cannot see the far end of.
var errRevealRefused = errors.New(
	"project bearer: refusing to print a credential to something that is not a terminal, " +
		"because a pipe, a file or a CI job carries it into whatever collects that output; " +
		"re-run at a terminal, or pass --allow-non-tty-reveal to take responsibility for the destination")

// mintBearer provisions or rotates, then shows the value exactly once.
//
// The reveal check runs before anything is written. Minting first and
// refusing to print afterwards would be the worst outcome available: the
// previous credential would already be dead and the new one unknowable,
// which is a self-inflicted lockout dressed as a safety check.
func mintBearer(ctx context.Context, st *store.Store, project store.Project,
	operation string, opts bearerOptions, stdout, stderr io.Writer) error {
	if !revealPermitted(opts.allowNonTTY) {
		return errRevealRefused
	}
	if opts.allowNonTTY && !revealPermitted(false) {
		note(stderr, "authstunt: warning: --allow-non-tty-reveal is printing a credential to a "+
			"destination this program cannot see the far end of. Whoever captures this output now holds it.")
	}

	var token string
	err := st.WithTx(ctx, func(tx *store.Tx) error {
		var err error
		if token, err = tx.SetProjectBearer(ctx, project.ID); err != nil {
			return err
		}
		// Same commit as the credential change, so the trail cannot be
		// missing the record of a rotation that happened. The event
		// carries the operation and nothing else - no value, no digest.
		_, err = ledger.Write(ctx, tx, ledger.Meta{ProjectID: project.ID}, ledger.BearerChanged{
			Operation: operation,
		})
		return err
	})
	if err != nil {
		return fmt.Errorf("project bearer: %w", err)
	}

	if operation == ledger.BearerRotated {
		note(stderr, "authstunt: the previous bearer no longer authenticates.")
	}
	note(stderr, "authstunt: store this now, it cannot be shown again:")
	// The only place a raw credential is ever written. It goes to stdout
	// alone on its line so an operator can read it, and it is deliberately
	// not routed through the logger: a log handler may ship somewhere this
	// must never reach.
	//
	// A failed write here is worth reporting rather than ignoring like the
	// notes above: the credential has already replaced whatever came
	// before it, so an operator who did not see the value needs to know
	// that rotating again is the only way out.
	if _, err := fmt.Fprintln(stdout, token); err != nil {
		return fmt.Errorf(
			"project bearer: the credential was written to the database but could not be printed, "+
				"so it is now unrecoverable; rotate again: %w", err)
	}
	return nil
}

// note writes an informational line to stderr.
//
// The error is dropped deliberately and in one place rather than at every
// call site: a diagnostic that cannot be written changes nothing about
// what the command did, and there is nowhere better to report it to.
func note(w io.Writer, line string) { _, _ = fmt.Fprintln(w, line) }

// revokeBearer clears the credential and records that it happened.
func revokeBearer(ctx context.Context, st *store.Store, project store.Project,
	has bool, stderr io.Writer) error {
	if !has {
		// The caller asked for a state that already holds. Writing a
		// ledger entry for a change that did not happen would put noise
		// into the one trail that has to stay trustworthy.
		note(stderr, "authstunt: this project has no bearer; nothing to revoke.")
		return nil
	}
	err := st.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.RevokeProjectBearer(ctx, project.ID); err != nil {
			return err
		}
		_, err := ledger.Write(ctx, tx, ledger.Meta{ProjectID: project.ID}, ledger.BearerChanged{
			Operation: ledger.BearerRevoked,
		})
		return err
	})
	if err != nil {
		return fmt.Errorf("project bearer: %w", err)
	}
	note(stderr,
		"authstunt: the bearer is revoked. The API cannot authenticate anybody until one is provisioned again.")
	return nil
}

// revealPermitted decides whether this process may print a credential.
//
// Two conditions, and both have to hold. stdout must be a terminal, which
// is the stream the value is written to, so the check measures the actual
// destination rather than a proxy for it. And CI must not be announcing
// itself: a runner that allocates a pty would otherwise pass the first
// check while still archiving every line it captures.
func revealPermitted(allowNonTTY bool) bool {
	if allowNonTTY {
		return true
	}
	return isTerminal(os.Stdout) && !inCI()
}

// isTerminal reports whether a stream is a character device.
//
// A terminal is one; a pipe, a file, and a socket are not. The check is
// the mode bit rather than an ioctl so it needs no dependency and behaves
// the same on Windows, where Go sets it for a console handle.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// inCI reports whether the environment announces itself as a CI runner.
//
// CI is the near-universal convention, set by GitHub Actions, GitLab CI,
// CircleCI and Travis among others. The falsy spellings are honored
// because some tooling sets the variable to say "no".
func inCI() bool {
	v := strings.TrimSpace(os.Getenv("CI"))
	return v != "" && !strings.EqualFold(v, "0") && !strings.EqualFold(v, "false")
}
