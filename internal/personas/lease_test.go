package personas_test

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivermin1123/authstunt/internal/personas"
	"github.com/ivermin1123/authstunt/internal/secrets"
	"github.com/ivermin1123/authstunt/internal/store"
)

func openStore(t *testing.T) (*store.Store, store.Project) {
	t.Helper()
	dir := t.TempDir()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "keys"), "test")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	s, err := store.Open(t.Context(), dir, key, store.Options{
		Logger: slog.New(slog.NewTextHandler(&strings.Builder{}, nil)),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	project, err := s.CreateProject(t.Context(), "test")
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if err := s.SetAllowlist(t.Context(), project.ID, []string{"demo.test"}); err != nil {
		t.Fatalf("allowlist: %v", err)
	}
	return s, project
}

// withPooledPolicy declares what the application under test is allowed to
// do with a reused address, which is the precondition for pooled mode
// existing at all. Every pooled test states it, because a service without
// one refuses to hand out a pooled identity.
func withPooledPolicy(maxDelivery time.Duration) func(*personas.Config) {
	return func(c *personas.Config) {
		c.Pooled = &personas.PooledPolicy{MaxDeliveryLatency: maxDelivery}
	}
}

func newService(t *testing.T, s *store.Store, project store.Project, opts ...func(*personas.Config)) *personas.Service {
	t.Helper()
	cfg := personas.Config{
		Store:     s,
		ProjectID: project.ID,
		Allowlist: []string{"demo.test"},
		Logger:    slog.New(slog.NewTextHandler(&strings.Builder{}, nil)),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	svc, err := personas.New(cfg)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc
}

// TestTwoRunsGetDistinctAddresses is the ephemeral mode's whole promise.
// Two runs that shared an address would see each other's mail, which is
// the failure the product exists to prevent.
func TestTwoRunsGetDistinctAddresses(t *testing.T) {
	s, project := openStore(t)
	svc := newService(t, s, project)
	ctx := t.Context()

	seen := map[string]bool{}
	const runs = 16
	for range runs {
		run, _, err := svc.CreateRun(ctx)
		if err != nil {
			t.Fatalf("create run: %v", err)
		}
		grant, err := svc.Acquire(ctx, run.ID, "pro", store.ModeEphemeral)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		if seen[grant.Addr] {
			t.Fatalf("address %q was granted to two runs", grant.Addr)
		}
		seen[grant.Addr] = true

		if !strings.HasSuffix(grant.Addr, "@demo.test") {
			t.Errorf("minted address %q escaped the allowlisted domain", grant.Addr)
		}
		if !strings.HasPrefix(grant.Addr, "pro-") {
			t.Errorf("minted address %q does not carry its role", grant.Addr)
		}
		// Without a seeder there is nothing to seed, and skipped is the
		// honest state for that. Pending must never reach a caller.
		if grant.SeedState != store.SeedStateSkipped {
			t.Errorf("seed state = %q, want skipped", grant.SeedState)
		}
	}
	if len(seen) != runs {
		t.Errorf("%d distinct addresses for %d runs", len(seen), runs)
	}
}

// TestMintedAddressCannotEscapeTheAllowlist pins the constraint from the
// other side: whatever a role is called, the address stays in the domain
// the project owns.
func TestMintedAddressCannotEscapeTheAllowlist(t *testing.T) {
	s, project := openStore(t)
	svc := newService(t, s, project)
	ctx := t.Context()

	hostile := []string{
		"pro@evil.test",
		"pro@evil.test\n",
		"../../etc",
		"pro>@evil",
		strings.Repeat("x", 100),
		"",
		"ADMIN",
	}
	for _, role := range hostile {
		run, _, err := svc.CreateRun(ctx)
		if err != nil {
			t.Fatalf("create run: %v", err)
		}
		grant, err := svc.Acquire(ctx, run.ID, role, store.ModeEphemeral)
		if err != nil {
			t.Fatalf("acquire with role %q: %v", role, err)
		}
		if !store.AllowlistMatches([]string{"demo.test"}, grant.Addr) {
			t.Errorf("role %q produced address %q, which is outside the allowlist", role, grant.Addr)
		}
		if strings.ContainsAny(grant.Addr, "\n\r <>\"") {
			t.Errorf("role %q produced address %q, which carries characters an address may not", role, grant.Addr)
		}
	}
}

// TestPooledAcquireIsExclusiveAndCoolsDown covers the pool: a second run
// cannot take a held identity, and a released one is held back for the
// cooldown so the previous run's late mail does not look like the next
// run's.
func TestPooledAcquireIsExclusiveAndCoolsDown(t *testing.T) {
	s, project := openStore(t)
	svc := newService(t, s, project, withPooledPolicy(time.Second), func(c *personas.Config) {
		c.PoolCooldown = time.Hour
	})
	ctx := t.Context()

	persona, err := s.CreatePersona(ctx, store.Persona{
		ProjectID: project.ID, Name: "pooled-pro", Email: "pooled-pro@demo.test",
		PasswordEnc: []byte("sealed"), Role: "pro",
	})
	if err != nil {
		t.Fatalf("persona: %v", err)
	}
	if _, err := s.CreateIdentity(ctx, store.NewIdentity{
		ProjectID: project.ID, PersonaID: persona.ID,
		Addr: "pooled-pro@demo.test", Role: "pro", Mode: store.ModePooled,
	}); err != nil {
		t.Fatalf("identity: %v", err)
	}

	first, _, err := svc.CreateRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := svc.Acquire(ctx, first.ID, "pro", store.ModePooled)
	if err != nil {
		t.Fatalf("first pooled acquire: %v", err)
	}
	if grant.Addr != "pooled-pro@demo.test" {
		t.Errorf("granted %q, want the pooled address", grant.Addr)
	}

	second, _, err := svc.CreateRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Acquire(ctx, second.ID, "pro", store.ModePooled); !errors.Is(err, personas.ErrNoIdentityAvailable) {
		t.Fatalf("acquire while the only pooled identity is held = %v, want ErrNoIdentityAvailable", err)
	}

	if err := svc.Release(ctx, first.ID, grant.LeaseID); err != nil {
		t.Fatalf("release: %v", err)
	}
	// Released, but cooling down: still not available.
	if _, err := svc.Acquire(ctx, second.ID, "pro", store.ModePooled); !errors.Is(err, personas.ErrNoIdentityAvailable) {
		t.Errorf("acquire inside the cooldown = %v, want ErrNoIdentityAvailable", err)
	}
}

// TestPooledIdentityIsReusableAfterTheCooldown is the other half: a
// cooldown that never ended would turn a pool into a one-shot list.
func TestPooledIdentityIsReusableAfterTheCooldown(t *testing.T) {
	s, project := openStore(t)
	svc := newService(t, s, project, withPooledPolicy(time.Second))
	ctx := t.Context()

	persona, err := s.CreatePersona(ctx, store.Persona{
		ProjectID: project.ID, Name: "pooled-pro", Email: "pooled-pro@demo.test",
		PasswordEnc: []byte("sealed"), Role: "pro",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateIdentity(ctx, store.NewIdentity{
		ProjectID: project.ID, PersonaID: persona.ID,
		Addr: "pooled-pro@demo.test", Role: "pro", Mode: store.ModePooled,
	}); err != nil {
		t.Fatal(err)
	}

	first, _, err := svc.CreateRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := svc.Acquire(ctx, first.ID, "pro", store.ModePooled)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := svc.Release(ctx, first.ID, grant.LeaseID); err != nil {
		t.Fatalf("release: %v", err)
	}

	// The cooldown is now at least a second long, because a pooled policy
	// is what makes it legal at all and one second is the shortest bound a
	// policy may declare. Waiting it out would put a real second into the
	// suite for no extra proof, so the deadline is moved into the past
	// instead: what this test is about is that an elapsed cooldown returns
	// the identity to the pool, not how the clock got there.
	if err := s.WithTx(ctx, func(tx *store.Tx) error {
		return tx.SetCooldown(ctx, grant.IdentityID, time.Now().Add(-time.Second))
	}); err != nil {
		t.Fatalf("elapse the cooldown: %v", err)
	}

	second, _, err := svc.CreateRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	again, err := svc.Acquire(ctx, second.ID, "pro", store.ModePooled)
	if err != nil {
		t.Fatalf("acquire after the cooldown: %v", err)
	}
	if again.IdentityID != grant.IdentityID {
		t.Errorf("a second identity appeared from a pool of one")
	}
}

// seedServer is a stand-in for the application under test.
type seedServer struct {
	mu       sync.Mutex
	requests []seedRequestBody
	handler  func(w http.ResponseWriter, r *http.Request)
}

type seedRequestBody struct {
	Addr           string `json:"addr"`
	Role           string `json:"role"`
	Mode           string `json:"mode"`
	RunID          string `json:"run_id"`
	LeaseID        string `json:"lease_id"`
	IdempotencyKey string `json:"-"`
}

func startSeedServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *seedServer) {
	t.Helper()
	rec := &seedServer{handler: handler}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body seedRequestBody
		_ = json.NewDecoder(r.Body).Decode(&body)
		body.IdempotencyKey = r.Header.Get("Idempotency-Key")
		rec.mu.Lock()
		rec.requests = append(rec.requests, body)
		rec.mu.Unlock()
		rec.handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func (r *seedServer) all() []seedRequestBody {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]seedRequestBody(nil), r.requests...)
}

func withSeeder(t *testing.T, url string, timeout time.Duration) func(*personas.Config) {
	t.Helper()
	seeder, err := personas.NewHTTPSeeder(url, timeout)
	if err != nil {
		t.Fatalf("seeder: %v", err)
	}
	return func(c *personas.Config) { c.Seeder = seeder }
}

func TestSeedSuccessIsRecordedOnTheLease(t *testing.T) {
	srv, rec := startSeedServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"seeded","fingerprint":"v1-abc"}`))
	})
	s, project := openStore(t)
	svc := newService(t, s, project, withSeeder(t, srv.URL, time.Second))
	ctx := t.Context()

	run, _, err := svc.CreateRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := svc.Acquire(ctx, run.ID, "pro", store.ModeEphemeral)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if grant.SeedState != store.SeedStateSeeded {
		t.Errorf("seed state = %q, want seeded", grant.SeedState)
	}

	lease, err := s.Lease(ctx, grant.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if !lease.Claimable() {
		t.Error("a seeded lease is not claimable")
	}
	if lease.SeedFingerprint != "v1-abc" {
		t.Errorf("fingerprint = %q", lease.SeedFingerprint)
	}

	reqs := rec.all()
	if len(reqs) != 1 {
		t.Fatalf("the seeder was called %d times, want 1", len(reqs))
	}
	if reqs[0].Addr != grant.Addr || reqs[0].LeaseID != grant.LeaseID {
		t.Errorf("the seed request did not describe the lease: %+v", reqs[0])
	}
	// The key is the lease id: stable across retries of this acquire, and
	// carrying no authority if the application logs it.
	if reqs[0].IdempotencyKey != grant.LeaseID {
		t.Errorf("Idempotency-Key = %q, want the lease id", reqs[0].IdempotencyKey)
	}
}

// TestSeedFailureBlocksClaim is the acceptance criterion: a lease whose
// seed failed must reach failed and must never be claimable.
func TestSeedFailureBlocksClaim(t *testing.T) {
	cases := []struct {
		name    string
		handler func(w http.ResponseWriter, r *http.Request)
	}{
		{"server error", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}},
		{"not json", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html>route not found</html>"))
		}},
		{"unknown status", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":"probably"}`))
		}},
		{"no status", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"fingerprint":"v1"}`))
		}},
		{"body past the cap", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":"seeded","fingerprint":"`))
			_, _ = w.Write([]byte(strings.Repeat("x", 70<<10)))
			_, _ = w.Write([]byte(`"}`))
		}},
		{"redirect", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://example.invalid/elsewhere", http.StatusTemporaryRedirect)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := startSeedServer(t, tc.handler)
			s, project := openStore(t)
			svc := newService(t, s, project, withSeeder(t, srv.URL, 5*time.Second))
			ctx := t.Context()

			run, _, err := svc.CreateRun(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Acquire(ctx, run.ID, "pro", store.ModeEphemeral); err == nil {
				t.Fatal("acquire reported success on a failed seed")
			}

			leases, err := s.ListRunLeases(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(leases) != 1 {
				t.Fatalf("%d leases recorded, want 1: a failed seed still has to leave evidence", len(leases))
			}
			if leases[0].SeedState != store.SeedStateFailed {
				t.Errorf("seed state = %q, want failed", leases[0].SeedState)
			}
			if leases[0].State != store.LeaseFailed {
				t.Errorf("lease state = %q, want failed", leases[0].State)
			}
			if leases[0].Claimable() {
				t.Error("a lease whose seed failed is claimable")
			}
			if leases[0].ReleasedAt.IsZero() {
				t.Error("a failed lease still occupies its identity")
			}
		})
	}
}

// TestSeedTimeoutFailsClosed covers the endpoint that never answers. The
// lease must not be left pending, because pending is a state only expiry
// would ever clean up.
func TestSeedTimeoutFailsClosed(t *testing.T) {
	release := make(chan struct{})
	srv, _ := startSeedServer(t, func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = w.Write([]byte(`{"status":"seeded"}`))
	})
	t.Cleanup(func() { close(release) })

	s, project := openStore(t)
	svc := newService(t, s, project, withSeeder(t, srv.URL, 100*time.Millisecond))
	ctx := t.Context()

	run, _, err := svc.CreateRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := svc.Acquire(ctx, run.ID, "pro", store.ModeEphemeral); err == nil {
		t.Fatal("acquire succeeded against an endpoint that never answered")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("acquire took %s: the seed timeout did not bound it", elapsed)
	}

	leases, err := s.ListRunLeases(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 || leases[0].SeedState != store.SeedStateFailed {
		t.Fatalf("lease after a seed timeout: %+v", leases)
	}
}

// TestSeedIsRetriedWithTheSameKey covers the replay the idempotency key
// exists for: two acquires that seed the same application account must be
// distinguishable by the application, and a retry of one acquire must not.
func TestSeedIsRetriedWithTheSameKey(t *testing.T) {
	srv, rec := startSeedServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"seeded","fingerprint":"v1"}`))
	})
	s, project := openStore(t)
	svc := newService(t, s, project, withSeeder(t, srv.URL, time.Second))
	ctx := t.Context()

	keys := map[string]bool{}
	for range 3 {
		run, _, err := svc.CreateRun(ctx)
		if err != nil {
			t.Fatal(err)
		}
		grant, err := svc.Acquire(ctx, run.ID, "pro", store.ModeEphemeral)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		keys[grant.LeaseID] = true
	}

	seen := map[string]bool{}
	for _, req := range rec.all() {
		if seen[req.IdempotencyKey] {
			t.Errorf("two different leases sent the same idempotency key %q", req.IdempotencyKey)
		}
		seen[req.IdempotencyKey] = true
		if !keys[req.IdempotencyKey] {
			t.Errorf("idempotency key %q is not a lease id", req.IdempotencyKey)
		}
	}
}

func TestSeedURLIsValidatedAtStartup(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"relative", "/seed"},
		{"no scheme", "example.test/seed"},
		{"wrong scheme", "ftp://example.test/seed"},
		{"file scheme", "file:///etc/passwd"},
		{"no host", "http:///seed"},
		{"userinfo", "http://user:pass@example.test/seed"},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := personas.NewHTTPSeeder(tc.url, time.Second); err == nil {
				t.Errorf("accepted seed url %q", tc.url)
			}
		})
	}
	if _, err := personas.NewHTTPSeeder("http://127.0.0.1:8080/seed", time.Second); err != nil {
		t.Errorf("rejected a usable seed url: %v", err)
	}
}

// TestSweepReleasesExpiredWork covers the sweeper the service exposes.
func TestSweepReleasesExpiredWork(t *testing.T) {
	s, project := openStore(t)
	svc := newService(t, s, project, func(c *personas.Config) {
		// A claim may not outlive its lease, so the three TTLs stay
		// ordered even when all of them are set to expire immediately.
		c.RunTTL = time.Nanosecond
		c.LeaseTTL = 2 * time.Nanosecond
		c.ClaimTTL = time.Nanosecond
	})
	ctx := t.Context()

	run, _, err := svc.CreateRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := svc.Acquire(ctx, run.ID, "pro", store.ModeEphemeral)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	runs, _, err := svc.Sweep(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if runs != 1 {
		t.Errorf("swept %d runs, want 1", runs)
	}
	lease, err := s.Lease(ctx, grant.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if lease.State == store.LeaseHeld {
		t.Error("the lease still holds its identity after the sweep")
	}

	// Idempotent: a second sweep finds nothing left to do.
	runs2, leases2, err := svc.Sweep(ctx)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if runs2 != 0 || leases2 != 0 {
		t.Errorf("a second sweep changed (%d runs, %d leases), want none", runs2, leases2)
	}
}

// TestEvidenceCarriesNoRawAddressOrSecret checks what the ledger keeps.
// Evidence has to be enough to debug a failure and not enough to be a
// mailing list, and the tokens must not be in it at all.
func TestEvidenceCarriesNoRawAddressOrSecret(t *testing.T) {
	s, project := openStore(t)
	svc := newService(t, s, project)
	ctx := t.Context()

	run, token, err := svc.CreateRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := svc.Acquire(ctx, run.ID, "pro", store.ModeEphemeral)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := svc.Release(ctx, run.ID, grant.LeaseID); err != nil {
		t.Fatalf("release: %v", err)
	}

	entries, err := s.ListLedger(ctx, store.LedgerFilter{ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Fatalf("%d ledger entries, want an acquire and a release at least", len(entries))
	}
	local, _, _ := strings.Cut(grant.Addr, "@")
	for _, e := range entries {
		if strings.Contains(e.DetailJSON, token) {
			t.Error("a ledger entry carries the run token")
		}
		if strings.Contains(e.DetailJSON, local) {
			t.Errorf("a ledger entry carries the full local part: %s", e.DetailJSON)
		}
		if !strings.Contains(e.DetailJSON, "demo.test") {
			t.Errorf("a ledger entry carries no address at all, so it cannot be debugged: %s", e.DetailJSON)
		}
	}
}

// TestNoShellExecutionOnTheSeedPath is the absence proof the phase
// requires. A seed hook that ran a command would make a configuration file
// a way to execute anything; the promise is a postcondition, and HTTP
// states one just as well.
func TestNoShellExecutionOnTheSeedPath(t *testing.T) {
	banned := map[string]string{
		"os/exec": "runs commands",
		"syscall": "can reach exec directly",
	}
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if why, bad := banned[path]; bad {
				t.Errorf("%s imports %s, which %s", name, path, why)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "exec" {
				t.Errorf("%s calls exec.%s", name, sel.Sel.Name)
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no source files were checked, so this proves nothing")
	}
}
