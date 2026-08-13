// Package personas turns a request for an identity of some role into an
// exclusive, seeded lease on a real address.
//
// The store enforces exclusivity; this package decides which identity to
// ask for, mints one when the mode calls for it, and drives the seed
// adapter. The split matters: an invariant enforced here would be an
// invariant two processes could break, and the one that matters - at most
// one run holds an identity at a time - is a unique index in SQL.
package personas

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ivermin1123/authstunt/internal/ledger"
	"github.com/ivermin1123/authstunt/internal/sse"
	"github.com/ivermin1123/authstunt/internal/store"
)

// Defaults for the durations the owner froze.
//
// DefaultPoolCooldown is hygiene, not a safety guarantee, and the
// difference is the whole point of PooledPolicy below: sixty seconds is a
// reasonable pause between two runs sharing an account, and it protects
// nothing on its own if the application's mail takes longer than that to
// arrive.
const (
	DefaultRunTTL       = 30 * time.Minute
	DefaultLeaseTTL     = 30 * time.Minute
	DefaultPoolCooldown = 60 * time.Second
	DefaultSeedTimeout  = 30 * time.Second
)

// MinDeliveryLatency is the smallest bound a pooled policy may declare.
//
// It is one second because that is the resolution of the only thing a
// message says about its own age: RFC 5322 dates have no finer field. A
// policy claiming tighter delivery than that is a number this server
// cannot check, so it is refused rather than accepted and ignored.
const MinDeliveryLatency = time.Second

// PooledPolicy is what an operator has to state before a pooled identity
// may be leased at all.
//
// Pooled mode hands the same address to one run after another, so the
// previous run's mail can still be in flight when the next run takes the
// address. There is no safe default for how long that takes: it is a
// property of the application under test, not of this server. Requiring
// the declaration is what turns a guessed cooldown into a stated bound
// that the handover guard and the cooldown floor are both derived from.
type PooledPolicy struct {
	// MaxDeliveryLatency is the operator's declared upper bound on the gap
	// between the run's last action and the resulting message being
	// generated and delivered here. It sets the cooldown floor.
	MaxDeliveryLatency time.Duration
}

// ErrPooledPolicyMissing refuses a pooled acquire in a service that was
// given no pooled policy.
//
// Refusing is the only honest answer. Serving the acquire would mean
// picking a delivery bound on the operator's behalf, and picking it wrong
// is what lets one run claim another run's code.
var ErrPooledPolicyMissing = errors.New("personas: pooled mode needs a declared safety policy (" +
	store.ReasonPooledPolicyMissing + ")")

// localPartBytes is the randomness in a minted local part. Six bytes is
// twelve hex characters, the same shape as every other id here, and far
// beyond collision range for the number of runs a test suite makes.
const localPartBytes = 6

// ErrNoIdentityAvailable reports that every pooled identity of a role is
// held or cooling down.
//
// It is a normal outcome, not a fault: a pool of four cannot serve five
// concurrent runs, and the caller's answer is to wait or to widen the
// pool.
var ErrNoIdentityAvailable = errors.New("personas: no identity of that role is available")

// Seeder prepares the application-side state a run needs before it can
// use an identity.
//
// The postcondition, not the mechanism, is what this promises: when Seed
// returns seeded, the account is in the state the run expects. Nothing
// here executes a shell; the only implementation is an HTTP call to an
// endpoint the application owns.
type Seeder interface {
	Seed(ctx context.Context, req SeedRequest) (SeedResult, error)
}

// SeedRequest is what the adapter is told about the lease it is seeding.
//
// IdempotencyKey is derived from the lease and nothing else. It is
// deliberately not the run token: a key travels to somebody else's server
// and may be logged there, so it must be a value that grants nothing.
type SeedRequest struct {
	RunID          string
	LeaseID        string
	IdentityID     string
	Addr           string
	Role           string
	Mode           string
	IdempotencyKey string
}

// SeedResult is a seeder's answer. Status is store.SeedStateSeeded or
// store.SeedStateSkipped; a failure is an error, not a status.
type SeedResult struct {
	Status      string
	Fingerprint string
}

// Config collects the lease service's dependencies.
type Config struct {
	Store     *store.Store
	ProjectID string
	// Allowlist is the project's stored domain patterns. Minted addresses
	// live under the first one, which is also where persona generation
	// takes its domain.
	Allowlist []string
	// Seeder is optional. Without one every lease settles as skipped,
	// which is the honest state for "nobody was asked to seed anything".
	Seeder   Seeder
	RunTTL   time.Duration
	LeaseTTL time.Duration
	// Pooled is required before any pooled identity can be leased, and is
	// ignored by ephemeral mode, whose addresses are minted per run and
	// never reused.
	Pooled *PooledPolicy
	// PoolCooldown is how long a pooled identity stays out of the pool
	// after a run gives it back. It may not be shorter than the policy's
	// declared delivery bound; a shorter value is refused here rather than
	// raised silently, because a safety parameter the operator did not
	// choose is one nobody knows the value of.
	PoolCooldown time.Duration
	// ClaimTTL is how long a claim can be replayed under its idempotency
	// key. Defaults to DefaultClaimTTL.
	ClaimTTL time.Duration
	// Bus is what a claim parks on while it waits for mail. Without one a
	// claim answers from what is already stored and never waits, which is
	// the honest behavior for a service nobody wired an event source to.
	Bus    *sse.Bus
	Logger *slog.Logger
}

// Service is the lease service.
type Service struct {
	store     *store.Store
	projectID string
	domain    string
	seeder    Seeder
	bus       *sse.Bus
	pooled    *PooledPolicy
	runTTL    time.Duration
	leaseTTL  time.Duration
	cooldown  time.Duration
	claimTTL  time.Duration
	logger    *slog.Logger
}

// Capabilities reports which optional dependencies the service was actually
// built with. Every one of them is allowed to be absent, and every one of them
// changes what a caller gets back, which is the combination that produces a
// degradation nobody notices.
//
// It reads the wired dependencies rather than the flags that were meant to
// wire them, so a capability lost to a wiring mistake reports off rather than
// reporting what the operator asked for.
type Capabilities struct {
	// LongPoll is whether a claim can park and wait for mail. Without it a
	// claim answers from what is already stored and timeout_ms is ignored.
	LongPoll bool
	// Pooled is whether pooled identities can be leased at all.
	Pooled bool
	// Seeder is whether a lease can be seeded. Without one every lease
	// settles as skipped.
	Seeder bool
}

// Capabilities returns what this service can actually do.
func (s *Service) Capabilities() Capabilities {
	return Capabilities{
		LongPoll: s.bus != nil,
		Pooled:   s.pooled != nil,
		Seeder:   s.seeder != nil,
	}
}

// New validates the configuration and returns the service.
func New(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, errors.New("personas: a store is required")
	}
	if cfg.ProjectID == "" {
		return nil, errors.New("personas: a project id is required")
	}
	if len(cfg.Allowlist) == 0 {
		return nil, errors.New("personas: an allowlist is required to mint addresses")
	}
	// A wildcard pattern contributes its base domain: "user@*.demo.test"
	// is not an address, and the base is allowlisted by the pattern that
	// declared it (design 4.2 item 17).
	domain, err := store.CanonicalDomain(store.PatternBaseDomain(cfg.Allowlist[0]))
	if err != nil {
		return nil, fmt.Errorf("personas: first allowlist entry is unusable: %w", err)
	}
	if cfg.RunTTL <= 0 {
		cfg.RunTTL = DefaultRunTTL
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = DefaultLeaseTTL
	}
	if cfg.ClaimTTL <= 0 {
		cfg.ClaimTTL = DefaultClaimTTL
	}
	// A claim that could outlive the lease it was granted under would be a
	// replay window nothing owns: the lease is gone, the identity may
	// already belong to another run, and the key would still return a
	// secret. The claim path refuses that on the gates anyway; refusing the
	// configuration is the lock that does not depend on gate order.
	if cfg.ClaimTTL >= cfg.LeaseTTL {
		return nil, fmt.Errorf("personas: claim TTL %s must be shorter than the lease TTL %s",
			cfg.ClaimTTL, cfg.LeaseTTL)
	}
	if err := cfg.resolveCooldown(); err != nil {
		return nil, err
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	// A nil bus is accepted on purpose so a test can build the service
	// without an event source, but it is not free: every claim answers from
	// what is already stored and returns at once, so a caller's timeout_ms
	// is accepted and ignored. Saying so here is what keeps that from being
	// discovered later as a long poll that never polls.
	if cfg.Bus == nil {
		cfg.Logger.Warn("personas: no event bus, so a claim cannot wait for mail; timeout_ms will be accepted and ignored")
	}
	return &Service{
		store:     cfg.Store,
		projectID: cfg.ProjectID,
		domain:    domain,
		seeder:    cfg.Seeder,
		bus:       cfg.Bus,
		pooled:    cfg.Pooled,
		runTTL:    cfg.RunTTL,
		leaseTTL:  cfg.LeaseTTL,
		cooldown:  cfg.PoolCooldown,
		claimTTL:  cfg.ClaimTTL,
		logger:    cfg.Logger,
	}, nil
}

// resolveCooldown validates the pooled configuration and settles the
// cooldown the service will use.
//
// Without a policy the cooldown is left as configured and is never
// consulted, because no pooled acquire can get far enough to need it. With
// one, the declared delivery bound is a floor: a shorter cooldown is
// refused here rather than raised to the floor, so an operator who thought
// they had a five-second handover learns it from a startup error instead
// of from a test that received somebody else's code.
func (cfg *Config) resolveCooldown() error {
	if cfg.PoolCooldown < 0 {
		return errors.New("personas: cooldown cannot be negative")
	}
	if cfg.Pooled == nil {
		if cfg.PoolCooldown == 0 {
			cfg.PoolCooldown = DefaultPoolCooldown
		}
		return nil
	}
	if cfg.Pooled.MaxDeliveryLatency < MinDeliveryLatency {
		return fmt.Errorf("personas: pooled policy declares a delivery bound of %s, minimum is %s",
			cfg.Pooled.MaxDeliveryLatency, MinDeliveryLatency)
	}
	floor := cfg.Pooled.MaxDeliveryLatency
	if cfg.PoolCooldown == 0 {
		cfg.PoolCooldown = max(DefaultPoolCooldown, floor)
		return nil
	}
	if cfg.PoolCooldown < floor {
		return fmt.Errorf("personas: pool cooldown %s is below the declared delivery bound %s (%s)",
			cfg.PoolCooldown, floor, store.ReasonPooledCooldownBelowFloor)
	}
	return nil
}

// CreateRun starts a run and returns it with its token. The token is
// returned once and never again.
func (s *Service) CreateRun(ctx context.Context) (store.Run, string, error) {
	return s.store.CreateRun(ctx, store.NewRun{ProjectID: s.projectID, TTL: s.runTTL})
}

// Grant is a successful acquire.
//
// SeedState is seeded or skipped and never pending: an acquire that
// returns has already settled, and a caller must never have to ask
// whether the account it was handed is ready.
type Grant struct {
	LeaseID    string
	IdentityID string
	Addr       string
	Role       string
	// Mode is the mode the identity actually carries, read back from the
	// row rather than echoed from the request.
	//
	// It exists so a surface can report what it served instead of what it
	// was asked for. An ephemeral request is never satisfied by a pooled
	// identity and vice versa, so the two agree today; reporting the
	// stored value is what keeps a future divergence visible instead of
	// silent.
	Mode      string
	SeedState string
	ExpiresAt time.Time
}

// Acquire grants a run exclusive use of an identity of the given role.
//
// It runs in three steps for a reason the owner froze: reserve inside a
// transaction, seed outside every transaction, settle inside another. The
// store has one write connection, so holding it across a 30-second call to
// somebody else's HTTP server would stall every other writer in the
// process, including the SMTP path that is answering live mail.
func (s *Service) Acquire(ctx context.Context, runID, role, mode string) (Grant, error) {
	lease, identity, err := s.reserve(ctx, runID, role, mode)
	if err != nil {
		return Grant{}, err
	}

	result, seedErr := s.seed(ctx, runID, lease, identity)

	// Settling happens whatever the seed said, because a lease left
	// pending is a lease nothing can ever claim against and nothing will
	// ever clean up except expiry.
	if err := s.settle(ctx, runID, lease, identity, result, seedErr); err != nil {
		return Grant{}, err
	}
	if seedErr != nil {
		return Grant{}, fmt.Errorf("personas: seeding %s failed: %w", identity.Addr, seedErr)
	}
	return Grant{
		LeaseID:    lease.ID,
		IdentityID: identity.ID,
		Addr:       identity.Addr,
		Role:       lease.Role,
		Mode:       identity.Mode,
		SeedState:  result.Status,
		ExpiresAt:  lease.ExpiresAt,
	}, nil
}

// EndRun moves a run to a terminal state and releases its leases.
//
// It exists so a surface never reaches past the service into the store:
// every other lifecycle step is a Service method, and an API handler that
// had to know about the store for exactly one of them would be the seam
// where that rule starts eroding.
func (s *Service) EndRun(ctx context.Context, runID, state, reason string) error {
	return s.store.EndRun(ctx, runID, state, reason)
}

// PooledPolicy reports the policy in force, or nil when pooled mode is
// not configured.
//
// A surface reports this so an operator can see that pooled mode is off
// rather than discovering it from a refusal.
func (s *Service) PooledPolicy() *PooledPolicy { return s.pooled }

// reserve takes the lease. For pooled mode it walks the candidates in
// order and lets the unique index decide, rather than reading "is it
// free" and acting on an answer that can be stale by the next statement.
func (s *Service) reserve(ctx context.Context, runID, role, mode string) (store.Lease, store.Identity, error) {
	switch mode {
	case store.ModeEphemeral:
		return s.reserveEphemeral(ctx, runID, role)
	case store.ModePooled:
		return s.reservePooled(ctx, runID, role)
	default:
		return store.Lease{}, store.Identity{}, fmt.Errorf("personas: %q is not an identity mode", mode)
	}
}

func (s *Service) reserveEphemeral(ctx context.Context, runID, role string) (store.Lease, store.Identity, error) {
	addr, err := s.mintAddress(role)
	if err != nil {
		return store.Lease{}, store.Identity{}, err
	}
	var (
		lease    store.Lease
		identity store.Identity
	)
	err = s.store.WithTx(ctx, func(tx *store.Tx) error {
		var err error
		identity, err = tx.CreateIdentity(ctx, store.NewIdentity{
			ProjectID: s.projectID,
			Addr:      addr,
			Role:      role,
			Mode:      store.ModeEphemeral,
		})
		if err != nil {
			return err
		}
		lease, err = tx.AcquireLease(ctx, store.NewLease{
			RunID: runID, IdentityID: identity.ID, Role: role, TTL: s.leaseTTL,
		})
		if err != nil {
			return err
		}
		return s.auditAcquire(ctx, tx, runID, lease, identity)
	})
	if err != nil {
		return store.Lease{}, store.Identity{}, err
	}
	return lease, identity, nil
}

func (s *Service) reservePooled(ctx context.Context, runID, role string) (store.Lease, store.Identity, error) {
	// The policy gate comes before the pool is even listed. A pooled
	// address outlives the run that used it, and without a declared
	// delivery bound there is no cooldown floor and no handover guard to
	// derive - so there is no configuration under which handing this
	// address over is known to be safe.
	if s.pooled == nil {
		if err := s.auditRefusal(ctx, runID, role, store.ModePooled,
			store.ReasonPooledPolicyMissing); err != nil {
			return store.Lease{}, store.Identity{}, err
		}
		return store.Lease{}, store.Identity{}, ErrPooledPolicyMissing
	}
	candidates, err := s.store.ListPooledIdentities(ctx, s.projectID, role)
	if err != nil {
		return store.Lease{}, store.Identity{}, err
	}
	now := time.Now()
	for _, candidate := range candidates {
		// Cooling down means the previous run's mail may still be
		// arriving for this address. Leasing it now would make that mail
		// look like it belongs to the next run.
		if !candidate.CooldownUntil.IsZero() && now.Before(candidate.CooldownUntil) {
			continue
		}
		var lease store.Lease
		err := s.store.WithTx(ctx, func(tx *store.Tx) error {
			var err error
			lease, err = tx.AcquireLease(ctx, store.NewLease{
				RunID: runID, IdentityID: candidate.ID, Role: role, TTL: s.leaseTTL,
			})
			if err != nil {
				return err
			}
			return s.auditAcquire(ctx, tx, runID, lease, candidate)
		})
		if err == nil {
			return lease, candidate, nil
		}
		// Losing the race is the expected outcome under concurrency: try
		// the next candidate rather than failing the whole acquire.
		if errors.Is(err, store.ErrIdentityHeld) {
			continue
		}
		return store.Lease{}, store.Identity{}, err
	}
	return store.Lease{}, store.Identity{}, fmt.Errorf("%w: role %q", ErrNoIdentityAvailable, role)
}

// seed calls the adapter, or reports skipped when there is none.
func (s *Service) seed(ctx context.Context, runID string, lease store.Lease, identity store.Identity) (SeedResult, error) {
	if s.seeder == nil {
		return SeedResult{Status: store.SeedStateSkipped}, nil
	}
	result, err := s.seeder.Seed(ctx, SeedRequest{
		RunID:      runID,
		LeaseID:    lease.ID,
		IdentityID: identity.ID,
		Addr:       identity.Addr,
		Role:       lease.Role,
		Mode:       identity.Mode,
		// The lease id is the key. It is stable across retries of the
		// same acquire, unique across leases, and grants nothing if the
		// receiving application logs it.
		IdempotencyKey: lease.ID,
	})
	if err != nil {
		return SeedResult{Status: store.SeedStateFailed}, err
	}
	switch result.Status {
	case store.SeedStateSeeded, store.SeedStateSkipped:
		return result, nil
	default:
		return SeedResult{Status: store.SeedStateFailed},
			fmt.Errorf("personas: seeder returned status %q", result.Status)
	}
}

// settle records the seed outcome, applies a cooldown when a pooled
// identity was left in an unknown state, and writes the evidence.
func (s *Service) settle(ctx context.Context, runID string, lease store.Lease, identity store.Identity, result SeedResult, seedErr error) error {
	state := result.Status
	if seedErr != nil {
		state = store.SeedStateFailed
	}
	return s.store.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.SettleSeed(ctx, lease.ID, state, result.Fingerprint); err != nil {
			// The lease can legitimately have lost its hold while the
			// seed was in flight: a sweeper may have expired it, or the
			// run may have ended. There is nothing left to settle, and
			// the ledger already records how it ended.
			if errors.Is(err, store.ErrLeaseNotHeld) {
				s.logger.Warn("personas: lease was no longer held when its seed settled",
					"lease_id", lease.ID, "seed_state", state)
				return nil
			}
			return err
		}
		if state == store.SeedStateFailed && identity.Mode == store.ModePooled {
			// A half-seeded account is not in a known state. Cooling it
			// down keeps the next run from inheriting the mess.
			if err := tx.SetCooldown(ctx, identity.ID, time.Now().Add(s.cooldown)); err != nil {
				return err
			}
		}
		event := ledger.SeedSettled{
			LeaseID:     lease.ID,
			IdentityID:  identity.ID,
			SeedState:   state,
			Addr:        ledger.Addr(identity.Addr),
			Fingerprint: result.Fingerprint,
		}
		if seedErr != nil {
			event.Reason = seedErr.Error()
		}
		_, err := ledger.Write(ctx, tx, s.meta(runID, identity), event)
		return err
	})
}

// Release gives an identity back and starts its cooldown if it is pooled.
func (s *Service) Release(ctx context.Context, runID, leaseID string) error {
	lease, err := s.store.Lease(ctx, leaseID)
	if err != nil {
		return err
	}
	identity, err := s.store.Identity(ctx, lease.IdentityID)
	if err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.ReleaseLease(ctx, leaseID); err != nil {
			return err
		}
		if identity.Mode == store.ModePooled {
			if err := tx.SetCooldown(ctx, identity.ID, time.Now().Add(s.cooldown)); err != nil {
				return err
			}
		}
		_, err := ledger.Write(ctx, tx, s.meta(runID, identity), ledger.LeaseReleased{
			LeaseID:    leaseID,
			IdentityID: identity.ID,
			Addr:       ledger.Addr(identity.Addr),
		})
		return err
	})
}

// Sweep expires runs and leases that have run out. It is idempotent and
// is safe to call on a timer and at every start.
func (s *Service) Sweep(ctx context.Context) (runs, leases int, err error) {
	runs, err = s.store.ExpireRuns(ctx)
	if err != nil {
		return 0, 0, err
	}
	leases, err = s.store.ExpireLeases(ctx)
	if err != nil {
		return runs, 0, err
	}
	return runs, leases, nil
}

// mintAddress builds a fresh address under the project's first allowlisted
// domain.
//
// The local part is distinct rather than a plus-suffix on a shared
// mailbox. Plus-addressing is the variant that depends on the application
// under test normalizing "+" the way the tool expects, and applications
// disagree; a wholly distinct local part needs nothing from anybody.
func (s *Service) mintAddress(role string) (string, error) {
	suffix := make([]byte, localPartBytes)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("personas: mint address: %w", err)
	}
	prefix := sanitizeRole(role)
	addr := prefix + "-" + hex.EncodeToString(suffix) + "@" + s.domain
	// The address is checked against the allowlist it was built from. The
	// check looks redundant and is not: the domain came from stored
	// configuration, and a minted address that could fall outside it
	// would be an address this project does not own.
	if !store.AllowlistMatches([]string{s.domain}, addr) {
		return "", fmt.Errorf("personas: minted address %q escaped the allowlisted domain", addr)
	}
	return addr, nil
}

// sanitizeRole reduces a role to characters that are safe in a local part.
// An empty or fully stripped role becomes "run", so the address still
// reads as something rather than starting with a bare hyphen.
func sanitizeRole(role string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(role) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "run"
	}
	return b.String()
}

// meta is the ledger context every event from this service shares.
func (s *Service) meta(runID string, identity store.Identity) ledger.Meta {
	return ledger.Meta{
		ProjectID: s.projectID,
		RunID:     runID,
		PersonaID: identity.PersonaID,
	}
}

// auditRefusal records an acquire that was refused before any identity was
// chosen.
//
// There is no address and no lease to name yet, which is why the event
// carries only the role, the mode and the reason code. An operator whose
// suite suddenly cannot acquire needs to find the reason in the evidence
// rather than in a log line that may not have been collected.
func (s *Service) auditRefusal(ctx context.Context, runID, role, mode, reason string) error {
	_, err := ledger.Write(ctx, s.store, ledger.Meta{ProjectID: s.projectID, RunID: runID},
		ledger.LeaseRefused{Role: role, Mode: mode, Reason: reason})
	return err
}

func (s *Service) auditAcquire(ctx context.Context, tx *store.Tx, runID string, lease store.Lease, identity store.Identity) error {
	_, err := ledger.Write(ctx, tx, s.meta(runID, identity), ledger.LeaseAcquired{
		LeaseID:    lease.ID,
		IdentityID: identity.ID,
		Role:       lease.Role,
		Mode:       identity.Mode,
		Addr:       ledger.Addr(identity.Addr),
	})
	return err
}
