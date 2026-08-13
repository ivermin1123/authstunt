package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/ivermin1123/authstunt/internal/api"
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
	apiListen    string
	apiHosts     domainList
	seedURL      string
	poolCooldown time.Duration
	pooledMaxLat time.Duration
	rotateBearer bool
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
	fs.StringVar(&opts.apiListen, "api-listen", api.DefaultAddr,
		"HTTP API listen address (PROVISIONAL surface: paths and fields may change before the phase 4b freeze)")
	fs.Var(&opts.apiHosts, "api-host",
		"additional Host header value the API accepts, repeatable; required to bind the API off loopback")
	fs.DurationVar(&opts.pooledMaxLat, "pooled-max-delivery-latency", 0,
		"declared upper bound on the application's mail delivery latency; enables pooled mode, which stays experimental")
	fs.BoolVar(&opts.rotateBearer, "rotate-bearer", false,
		"REMOVED: use `authstunt project bearer rotate` instead; serve never mints or prints a credential")
	fs.StringVar(&opts.seedURL, "seed-url", "",
		"absolute http or https URL the application exposes to seed a leased identity; leaving it unset skips seeding")
	fs.DurationVar(&opts.poolCooldown, "pool-cooldown", personas.DefaultPoolCooldown,
		"how long a released pooled identity is held back before it can be leased again")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if opts.rotateBearer {
		return errRotateBearerRemoved
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
	st, err := openDataDir(ctx, dataDir, logger)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			logger.Error("serve: closing the store failed", "error", err)
		}
	}()

	project, allowlist, err := bootstrap(ctx, st, opts.project, opts.domains)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
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

	apiSrv, apiListener, err := startAPI(ctx, st, leases, project, opts, logger)
	if err != nil {
		return err
	}
	apiErr := make(chan error, 1)
	if apiSrv != nil {
		go func() {
			err := apiSrv.Serve(apiListener)
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			apiErr <- err
		}()
	}

	// Printed on stdout, and printed last, so a fixture can wait for this
	// line and know both ports are already accepting. The SMTP address
	// stays the final field: fixtures already parse it from the end of
	// this line, and moving it would break them for no gain.
	apiAddr := "off"
	if apiListener != nil {
		apiAddr = apiListener.Addr().String()
	}
	fmt.Printf("authstunt serving project %s, api %s, smtp %s\n", project.Name, apiAddr, srv.Addr())

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
	if apiSrv != nil {
		if err := apiSrv.Shutdown(shutdownCtx); err != nil {
			logger.Error("serve: shutting down the api listener failed", "error", err)
		}
		if err := <-apiErr; err != nil {
			logger.Error("serve: the api listener stopped with an error", "error", err)
		}
	}
	ingest.Stop()
	return serveErr
}

// startAPI binds the provisional HTTP surface.
//
// It returns a nil server when --api-listen is empty, which is how an
// operator runs a mail-catcher-only instance.
func startAPI(ctx context.Context, st *store.Store, leases *personas.Service,
	project store.Project, opts serveOptions, logger *slog.Logger) (*http.Server, net.Listener, error) {
	if opts.apiListen == "" {
		return nil, nil, nil
	}
	if err := guardAPIBind(opts); err != nil {
		return nil, nil, err
	}
	if err := requireBearer(ctx, st, project); err != nil {
		return nil, nil, err
	}

	srv, err := api.New(api.Config{
		Store:        st,
		Service:      leases,
		ProjectID:    project.ID,
		AllowedHosts: apiHosts(opts),
		Version:      version,
		Logger:       logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("serve: %w", err)
	}
	httpSrv := srv.HTTPServer(opts.apiListen)
	listener, err := net.Listen("tcp", opts.apiListen)
	if err != nil {
		return nil, nil, fmt.Errorf("serve: api listen %s: %w", opts.apiListen, err)
	}
	return httpSrv, listener, nil
}

// guardAPIBind refuses to start on a non-loopback address unless the
// operator named the hosts it should answer to.
//
// The contract mirrors SMTP's: binding a credential-issuing surface to
// something the network can reach is a decision, and a decision has to be
// written down. --api-host is that writing. Without it a rebound or
// misdirected request would arrive with a Host nobody vetted.
func guardAPIBind(opts serveOptions) error {
	host, _, err := net.SplitHostPort(opts.apiListen)
	if err != nil {
		return fmt.Errorf("serve: --api-listen %q is not host:port: %w", opts.apiListen, err)
	}
	if len(opts.apiHosts) > 0 {
		return nil
	}
	// An empty host means every interface, which is the widest bind there
	// is and never accidental.
	if host == "" {
		return errors.New("serve: --api-listen binds every interface; name the Host values to accept with --api-host")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// A name rather than a literal: it may resolve anywhere, so it is
		// treated as non-loopback.
		return fmt.Errorf("serve: --api-listen %q is not a loopback address; add --api-host", host)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("serve: --api-listen %q is not a loopback address; add --api-host", host)
	}
	return nil
}

// apiHosts returns the Host allowlist, letting the package default cover
// the loopback names when the operator named none.
func apiHosts(opts serveOptions) []string {
	if len(opts.apiHosts) == 0 {
		return nil
	}
	return append([]string(nil), opts.apiHosts...)
}

// errRotateBearerRemoved answers the flag that used to rotate the
// credential during startup.
//
// The flag is still registered rather than deleted so that an operator or
// a script carrying it gets this sentence instead of "flag provided but
// not defined", which says nothing about where the capability went.
var errRotateBearerRemoved = errors.New(
	"serve: --rotate-bearer has been removed because serve must never emit a credential; " +
		"run `authstunt project bearer rotate --data-dir <dir>` instead")

// errNoBearer is the startup refusal for an API that has no credential to
// authenticate anyone with.
var errNoBearer = errors.New(
	"serve: this project has no API bearer; " +
		"run `authstunt project bearer provision --data-dir <dir>` first, " +
		"or pass --api-listen \"\" to run as a mail catcher only")

// requireBearer refuses to serve the API for a project nobody provisioned.
//
// Failing closed here is the point of the whole shape. The alternative
// serve used to take - mint one and print it - meant every start was a
// potential disclosure into whatever collects a process's output: a CI
// log, a supervisor journal, a container runtime, a shipping agent. None
// of those are things this process can see or reason about, so the only
// safe rule is that serve never holds a raw credential at all. Provisioning
// moved to a command an operator runs deliberately, at a terminal, once.
func requireBearer(ctx context.Context, st *store.Store, project store.Project) error {
	has, err := st.ProjectHasBearer(ctx, project.ID)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	if !has {
		return errNoBearer
	}
	return nil
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
	// Pooled mode stays off until the operator declares the application's
	// delivery bound. The debt this pays off is from phase 3: the service
	// could already be built with a policy, but the binary had no flag to
	// express one, so pooled mode was unreachable from a shipped build.
	// It remains conditional and experimental; ephemeral is the default.
	if opts.pooledMaxLat > 0 {
		cfg.Pooled = &personas.PooledPolicy{MaxDeliveryLatency: opts.pooledMaxLat}
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

// openDataDir loads the project key and opens the store.
//
// Both commands that touch a data directory go through here, so the key
// file and the database are always opened the same way and in the same
// order. Callers add their own prefix to the error: the failure is the
// same, but which command hit it is not.
func openDataDir(ctx context.Context, dataDir string, logger *slog.Logger) (*store.Store, error) {
	key, err := secrets.LoadOrCreateKey(filepath.Join(dataDir, "keys"), keyName)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(ctx, dataDir, key, store.Options{Logger: logger})
	if err != nil {
		return nil, err
	}
	return st, nil
}

// bootstrap applies design 4.2 item 2: create the project and its ordered
// allowlist on an uninitialized directory, verify them on an initialized
// one, and never reconcile silently.
//
// It is shared by serve and by the bearer command, which is why it takes
// the two values rather than the serve flag set: provisioning a credential
// on a directory nobody initialized yet has to initialize it the same way,
// through this function, not through a second copy of these rules.
func bootstrap(ctx context.Context, st *store.Store, name string, domains []string) (store.Project, []string, error) {
	existing, err := st.ListProjects(ctx)
	if err != nil {
		return store.Project{}, nil, err
	}

	switch len(existing) {
	case 0:
		if name == "" || len(domains) == 0 {
			return store.Project{}, nil, errors.New(
				"this data directory is empty: --project and at least one --domain are required to initialize it")
		}
		project, err := st.CreateProject(ctx, name)
		if err != nil {
			return store.Project{}, nil, err
		}
		if err := st.SetAllowlist(ctx, project.ID, domains); err != nil {
			return store.Project{}, nil, err
		}
		allowlist, err := st.Allowlist(ctx, project.ID)
		if err != nil {
			return store.Project{}, nil, err
		}
		return project, allowlist, nil
	case 1:
		project := existing[0]
		allowlist, err := st.Allowlist(ctx, project.ID)
		if err != nil {
			return store.Project{}, nil, err
		}
		if err := verifyFlags(project, allowlist, name, domains); err != nil {
			return store.Project{}, nil, err
		}
		return project, allowlist, nil
	default:
		// The schema can hold several projects; a running instance owns
		// one. More than one means the directory was built by something
		// this binary does not understand, and guessing which row to
		// serve would be worse than stopping.
		return store.Project{}, nil, fmt.Errorf(
			"this data directory holds %d projects, but an instance serves exactly one", len(existing))
	}
}

// verifyFlags refuses a start whose flags disagree with what is stored.
//
// Reconciliation is a separate, explicit surface. A serve that quietly
// rewrote an allowlist would be a serve that can quietly widen one.
func verifyFlags(project store.Project, allowlist []string, name string, domains []string) error {
	if name != "" && name != project.Name {
		return fmt.Errorf("--project %q does not match the stored project %q", name, project.Name)
	}
	if len(domains) == 0 {
		return nil
	}
	given := make([]string, 0, len(domains))
	for _, d := range domains {
		canonical, err := store.CanonicalDomainPattern(d)
		if err != nil {
			return fmt.Errorf("--domain %q: %w", d, err)
		}
		given = append(given, canonical)
	}
	if !slices.Equal(given, allowlist) {
		return fmt.Errorf("--domain flags %v do not match the stored allowlist %v", given, allowlist)
	}
	return nil
}
