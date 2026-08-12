package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/ivermin1123/authstunt/internal/personas"
	"github.com/ivermin1123/authstunt/internal/secrets"
	"github.com/ivermin1123/authstunt/internal/smtp"
	"github.com/ivermin1123/authstunt/internal/sse"
	"github.com/ivermin1123/authstunt/internal/store"
)

// keyName is the key file's name inside the data directory.
//
// It is fixed rather than derived from the project, because a data
// directory holds exactly one project and one key (design 4.2 item 1). A
// name derived from the project would also be unreadable on a started
// instance that omitted --project, which the bootstrap contract allows
// once the directory is initialized.
const keyName = "project"

// shutdownGrace bounds how long a stop waits for connections and workers.
const shutdownGrace = 10 * time.Second

// domainList collects a repeatable --domain flag in the order given,
// because the allowlist is ordered and persona generation takes the first
// entry.
type domainList []string

func (d *domainList) String() string { return strings.Join(*d, ",") }

func (d *domainList) Set(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return errors.New("empty domain")
	}
	*d = append(*d, v)
	return nil
}

type serveOptions struct {
	project      string
	domains      domainList
	dataDir      string
	smtpListen   string
	seedURL      string
	poolCooldown time.Duration
}

// runServe is the serve subcommand.
func runServe(args []string, stderr io.Writer) error {
	var opts serveOptions
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.project, "project", "", "project name (required on an uninitialized data dir)")
	fs.Var(&opts.domains, "domain", "allowlisted domain pattern, repeatable; a leading '*.' covers subdomains")
	fs.StringVar(&opts.dataDir, "data-dir", "", "data directory (default ~/.authstunt/<project>)")
	fs.StringVar(&opts.smtpListen, "smtp-listen", "127.0.0.1:1025", "SMTP listen address")
	fs.StringVar(&opts.seedURL, "seed-url", "",
		"absolute http or https URL the application exposes to seed a leased identity; leaving it unset skips seeding")
	fs.DurationVar(&opts.poolCooldown, "pool-cooldown", personas.DefaultPoolCooldown,
		"how long a released pooled identity is held back before it can be leased again")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dataDir, err := resolveDataDir(opts)
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return serve(ctx, opts, dataDir, logger)
}

// resolveDataDir applies the documented default of ~/.authstunt/<project>.
func resolveDataDir(opts serveOptions) (string, error) {
	if opts.dataDir != "" {
		return opts.dataDir, nil
	}
	if opts.project == "" {
		return "", errors.New("serve: --project or --data-dir is required")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("serve: home directory: %w", err)
	}
	return filepath.Join(home, ".authstunt", opts.project), nil
}

// serve opens the data directory, applies the bootstrap contract, and runs
// until the context is canceled.
func serve(ctx context.Context, opts serveOptions, dataDir string, logger *slog.Logger) error {
	key, err := secrets.LoadOrCreateKey(filepath.Join(dataDir, "keys"), keyName)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	st, err := store.Open(ctx, dataDir, key, store.Options{Logger: logger})
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			logger.Error("serve: closing the store failed", "error", err)
		}
	}()

	project, allowlist, err := bootstrap(ctx, st, opts)
	if err != nil {
		return err
	}

	// The lease service is built before anything starts listening, so a
	// bad --seed-url is a startup error rather than a surprise the first
	// time a run asks for an identity.
	leases, err := newLeaseService(st, project, allowlist, opts, logger)
	if err != nil {
		return err
	}
	// Sweeping at start is what makes the expiry contract safe after a
	// crash: a process that died holding leases left identities locked,
	// and nobody is coming to unlock them.
	if runs, held, err := leases.Sweep(ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	} else if runs > 0 || held > 0 {
		logger.Info("serve: swept expired work from a previous run",
			"runs", runs, "leases", held)
	}

	generation, err := st.NextEventGeneration(ctx)
	if err != nil {
		return fmt.Errorf("serve: event generation: %w", err)
	}
	bus := sse.NewBus(generation)
	busCtx, stopBus := context.WithCancel(context.Background())
	defer stopBus()
	go bus.Run(busCtx)

	ingest, err := smtp.NewIngest(smtp.IngestConfig{
		Store:     st,
		Bus:       bus,
		ProjectID: project.ID,
		Allowlist: allowlist,
		Logger:    logger,
	})
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	// Workers start before recovery so a pending row settles through the
	// same path a live message takes, and before the listener binds so a
	// message that arrives immediately is never the first thing tried.
	ingest.Start(ctx)
	if err := ingest.Recover(ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	}

	srv, err := smtp.NewServer(smtp.Config{
		Addr:      opts.smtpListen,
		Deliverer: ingest,
		Logger:    logger,
	})
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	if err := srv.Listen(); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	// Printed on stdout, and printed last, so a fixture can wait for this
	// line and know the port is already accepting.
	fmt.Printf("authstunt serving project %s, smtp %s\n", project.Name, srv.Addr())

	serveErr := srv.Serve(ctx)

	// Shutdown ordering matters and is the reverse of startup: stop
	// accepting, drain the sessions still inside Data, then drain the
	// extraction workers they fed. Stopping the workers first would close
	// the queue under a session that is still allowed to publish to it.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("serve: shutting down the smtp listener failed", "error", err)
	}
	ingest.Stop()
	return serveErr
}

// newLeaseService wires the lease service, including the seed adapter
// when one is configured.
func newLeaseService(st *store.Store, project store.Project, allowlist []string,
	opts serveOptions, logger *slog.Logger) (*personas.Service, error) {
	cfg := personas.Config{
		Store:        st,
		ProjectID:    project.ID,
		Allowlist:    allowlist,
		PoolCooldown: opts.poolCooldown,
		Logger:       logger,
	}
	if opts.seedURL != "" {
		seeder, err := personas.NewHTTPSeeder(opts.seedURL, personas.DefaultSeedTimeout)
		if err != nil {
			return nil, fmt.Errorf("serve: %w", err)
		}
		cfg.Seeder = seeder
	}
	svc, err := personas.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("serve: %w", err)
	}
	return svc, nil
}

// bootstrap applies design 4.2 item 2: create the project and its ordered
// allowlist on an uninitialized directory, verify them on an initialized
// one, and never reconcile silently.
func bootstrap(ctx context.Context, st *store.Store, opts serveOptions) (store.Project, []string, error) {
	existing, err := st.ListProjects(ctx)
	if err != nil {
		return store.Project{}, nil, fmt.Errorf("serve: %w", err)
	}

	switch len(existing) {
	case 0:
		if opts.project == "" || len(opts.domains) == 0 {
			return store.Project{}, nil, errors.New(
				"serve: this data directory is empty: --project and at least one --domain are required to initialize it")
		}
		project, err := st.CreateProject(ctx, opts.project)
		if err != nil {
			return store.Project{}, nil, fmt.Errorf("serve: %w", err)
		}
		if err := st.SetAllowlist(ctx, project.ID, opts.domains); err != nil {
			return store.Project{}, nil, fmt.Errorf("serve: %w", err)
		}
		allowlist, err := st.Allowlist(ctx, project.ID)
		if err != nil {
			return store.Project{}, nil, fmt.Errorf("serve: %w", err)
		}
		return project, allowlist, nil
	case 1:
		project := existing[0]
		allowlist, err := st.Allowlist(ctx, project.ID)
		if err != nil {
			return store.Project{}, nil, fmt.Errorf("serve: %w", err)
		}
		if err := verifyFlags(project, allowlist, opts); err != nil {
			return store.Project{}, nil, err
		}
		return project, allowlist, nil
	default:
		// The schema can hold several projects; a running instance owns
		// one. More than one means the directory was built by something
		// this binary does not understand, and guessing which row to
		// serve would be worse than stopping.
		return store.Project{}, nil, fmt.Errorf(
			"serve: this data directory holds %d projects, but an instance serves exactly one", len(existing))
	}
}

// verifyFlags refuses a start whose flags disagree with what is stored.
//
// Reconciliation is a separate, explicit surface. A serve that quietly
// rewrote an allowlist would be a serve that can quietly widen one.
func verifyFlags(project store.Project, allowlist []string, opts serveOptions) error {
	if opts.project != "" && opts.project != project.Name {
		return fmt.Errorf("serve: --project %q does not match the stored project %q",
			opts.project, project.Name)
	}
	if len(opts.domains) == 0 {
		return nil
	}
	given := make([]string, 0, len(opts.domains))
	for _, d := range opts.domains {
		canonical, err := store.CanonicalDomainPattern(d)
		if err != nil {
			return fmt.Errorf("serve: --domain %q: %w", d, err)
		}
		given = append(given, canonical)
	}
	if !slices.Equal(given, allowlist) {
		return fmt.Errorf("serve: --domain flags %v do not match the stored allowlist %v",
			given, allowlist)
	}
	return nil
}
