package store_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivermin1123/authstunt/internal/store"
)

const (
	runTTL   = 30 * time.Minute
	leaseTTL = 10 * time.Minute
)

func newRun(t *testing.T, s *store.Store, projectID string) (store.Run, string) {
	t.Helper()
	run, token, err := s.CreateRun(t.Context(), store.NewRun{ProjectID: projectID, TTL: runTTL})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	return run, token
}

func newIdentity(t *testing.T, s *store.Store, projectID, addr string) store.Identity {
	t.Helper()
	id, err := s.CreateIdentity(t.Context(), store.NewIdentity{
		ProjectID: projectID,
		Addr:      addr,
		Role:      "pro",
		Mode:      store.ModeEphemeral,
	})
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}
	return id
}

func acquire(t *testing.T, s *store.Store, runID, identityID string) (store.Lease, error) {
	t.Helper()
	var lease store.Lease
	err := s.WithTx(t.Context(), func(tx *store.Tx) error {
		var err error
		lease, err = tx.AcquireLease(t.Context(), store.NewLease{
			RunID: runID, IdentityID: identityID, Role: "pro", TTL: leaseTTL,
		})
		return err
	})
	return lease, err
}

// TestOnlyOneLeaseHeldPerIdentity is the phase's central claim. Sixty-four
// goroutines race for one identity and exactly one may win, and the reason
// it holds is that the loser is rejected by a unique index rather than by
// a read that could have been stale by the time it was acted on.
func TestOnlyOneLeaseHeldPerIdentity(t *testing.T) {
	s := openTestStore(t)
	project := newProject(t, s)
	identity := newIdentity(t, s, project.ID, "pool-a@demo.test")

	const racers = 64
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners []store.Lease
		losses  int
		other   []error
	)
	start := make(chan struct{})
	for range racers {
		run, _ := newRun(t, s, project.ID)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			lease, err := acquire(t, s, run.ID, identity.ID)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				winners = append(winners, lease)
			case errors.Is(err, store.ErrIdentityHeld):
				losses++
			default:
				other = append(other, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(other) > 0 {
		t.Fatalf("unexpected errors: %v", other)
	}
	if len(winners) != 1 {
		t.Fatalf("%d acquirers won, want exactly 1", len(winners))
	}
	if losses != racers-1 {
		t.Errorf("%d acquirers lost the race, want %d", losses, racers-1)
	}
	if winners[0].SeedState != store.SeedStatePending {
		t.Errorf("a fresh lease has seed state %q, want pending", winners[0].SeedState)
	}
	if winners[0].Claimable() {
		t.Error("a lease whose seed has not settled is claimable")
	}
}

// TestReleaseFreesTheIdentityForTheNextRun is the other half: exclusivity
// that never ends would be a leak, not a guarantee.
func TestReleaseFreesTheIdentityForTheNextRun(t *testing.T) {
	s := openTestStore(t)
	project := newProject(t, s)
	identity := newIdentity(t, s, project.ID, "pool-b@demo.test")

	first, _ := newRun(t, s, project.ID)
	lease, err := acquire(t, s, first.ID, identity.ID)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	second, _ := newRun(t, s, project.ID)
	if _, err := acquire(t, s, second.ID, identity.ID); !errors.Is(err, store.ErrIdentityHeld) {
		t.Fatalf("second acquire while held = %v, want ErrIdentityHeld", err)
	}

	if err := s.ReleaseLease(t.Context(), lease.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := acquire(t, s, second.ID, identity.ID); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}

	// Releasing twice is what a fixture's cleanup does when the test body
	// already released. It must not fail the teardown.
	if err := s.ReleaseLease(t.Context(), lease.ID); err != nil {
		t.Errorf("second release: %v", err)
	}
}

// TestReleasedLeaseKeepsItsRow pins the reason release is not a delete:
// correlation reads which lease owned an address at a past instant, and a
// deleted row would make that history lie.
func TestReleasedLeaseKeepsItsRow(t *testing.T) {
	s := openTestStore(t)
	project := newProject(t, s)
	identity := newIdentity(t, s, project.ID, "pool-c@demo.test")
	run, _ := newRun(t, s, project.ID)

	lease, err := acquire(t, s, run.ID, identity.ID)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := s.ReleaseLease(t.Context(), lease.ID); err != nil {
		t.Fatalf("release: %v", err)
	}

	after, err := s.Lease(t.Context(), lease.ID)
	if err != nil {
		t.Fatalf("read back a released lease: %v", err)
	}
	if after.State != store.LeaseReleased {
		t.Errorf("state = %q, want released", after.State)
	}
	if after.ReleasedAt.IsZero() {
		t.Error("a released lease has no release time, so no interval can be resolved against it")
	}
}

func TestSeedFailureReleasesTheLeaseAndBlocksClaims(t *testing.T) {
	s := openTestStore(t)
	project := newProject(t, s)
	identity := newIdentity(t, s, project.ID, "pool-d@demo.test")
	run, _ := newRun(t, s, project.ID)

	lease, err := acquire(t, s, run.ID, identity.ID)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	err = s.WithTx(t.Context(), func(tx *store.Tx) error {
		return tx.SettleSeed(t.Context(), lease.ID, store.SeedStateFailed, "")
	})
	if err != nil {
		t.Fatalf("settle seed: %v", err)
	}

	after, err := s.Lease(t.Context(), lease.ID)
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if after.State != store.LeaseFailed {
		t.Errorf("state = %q, want failed", after.State)
	}
	if after.ReleasedAt.IsZero() {
		t.Error("a failed lease was not released, so it still occupies the identity")
	}
	if after.Claimable() {
		t.Error("a lease whose seed failed is claimable")
	}

	// The identity is free again, because the exclusivity index only
	// covers held leases.
	next, _ := newRun(t, s, project.ID)
	if _, err := acquire(t, s, next.ID, identity.ID); err != nil {
		t.Errorf("acquire after a failed seed: %v", err)
	}
}

func TestSeedSuccessMakesTheLeaseClaimable(t *testing.T) {
	for _, outcome := range []string{store.SeedStateSeeded, store.SeedStateSkipped} {
		t.Run(outcome, func(t *testing.T) {
			s := openTestStore(t)
			project := newProject(t, s)
			identity := newIdentity(t, s, project.ID, "pool-"+outcome+"@demo.test")
			run, _ := newRun(t, s, project.ID)

			lease, err := acquire(t, s, run.ID, identity.ID)
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}
			err = s.WithTx(t.Context(), func(tx *store.Tx) error {
				return tx.SettleSeed(t.Context(), lease.ID, outcome, "fp-1")
			})
			if err != nil {
				t.Fatalf("settle seed: %v", err)
			}

			after, err := s.Lease(t.Context(), lease.ID)
			if err != nil {
				t.Fatalf("lease: %v", err)
			}
			if !after.Claimable() {
				t.Errorf("lease with seed %q is not claimable", outcome)
			}
			if after.State != store.LeaseHeld {
				t.Errorf("state = %q, want held", after.State)
			}
			if after.SeedFingerprint != "fp-1" {
				t.Errorf("fingerprint = %q", after.SeedFingerprint)
			}
		})
	}
}

// TestSettleSeedRefusesALeaseThatLostItsHold covers the crash-and-expire
// window: the seed adapter can return long after a sweeper released the
// lease, and settling it then would resurrect a hold nobody has.
func TestSettleSeedRefusesALeaseThatLostItsHold(t *testing.T) {
	s := openTestStore(t)
	project := newProject(t, s)
	identity := newIdentity(t, s, project.ID, "pool-e@demo.test")
	run, _ := newRun(t, s, project.ID)

	lease, err := acquire(t, s, run.ID, identity.ID)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := s.ReleaseLease(t.Context(), lease.ID); err != nil {
		t.Fatalf("release: %v", err)
	}

	err = s.WithTx(t.Context(), func(tx *store.Tx) error {
		return tx.SettleSeed(t.Context(), lease.ID, store.SeedStateSeeded, "late")
	})
	if !errors.Is(err, store.ErrLeaseNotHeld) {
		t.Fatalf("settling a released lease = %v, want ErrLeaseNotHeld", err)
	}
}

// TestLeaseAtResolvesTheOwningInterval is the correlation rule phase 3 is
// built on, tested here where the rows live.
func TestLeaseAtResolvesTheOwningInterval(t *testing.T) {
	// The clock is stepped rather than real, so the two intervals have a
	// width the assertions can address. Under a real clock an acquire and
	// its release can land inside the same millisecond, and the interval
	// they describe is then a single tick wide: correct, and useless for
	// proving where the boundaries are.
	clock := steppedClock(time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC), time.Second)
	s := openTestStoreWithClock(t, clock)
	project := newProject(t, s)
	identity := newIdentity(t, s, project.ID, "shared@demo.test")

	first, _ := newRun(t, s, project.ID)
	lease1, err := acquire(t, s, first.ID, identity.ID)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := s.ReleaseLease(t.Context(), lease1.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	released1, err := s.Lease(t.Context(), lease1.ID)
	if err != nil {
		t.Fatalf("lease: %v", err)
	}

	second, _ := newRun(t, s, project.ID)
	lease2, err := acquire(t, s, second.ID, identity.ID)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}

	cases := []struct {
		name string
		at   time.Time
		want string
	}{
		{"before anything held it", released1.AcquiredAt.Add(-time.Second), ""},
		{"at the first acquire instant", released1.AcquiredAt, lease1.ID},
		{"inside the first lease", released1.AcquiredAt.Add(time.Nanosecond), lease1.ID},
		// The interval is half-open, so the release instant already
		// belongs to nobody. That is not an edge case to round away: the
		// gap between one run releasing and the next acquiring is real,
		// and mail that lands in it genuinely has no owner. Phase 3
		// records exactly that as an unbound recipient rather than
		// guessing which side of the gap it belonged to.
		{"at the release instant, in the gap", released1.ReleasedAt, ""},
		{"just before the second acquire", lease2.AcquiredAt.Add(-time.Nanosecond), ""},
		{"at the second acquire instant", lease2.AcquiredAt, lease2.ID},
		{"inside the second lease", lease2.AcquiredAt.Add(time.Second), lease2.ID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.LeaseAt(t.Context(), "SHARED@demo.test", tc.at)
			if tc.want == "" {
				if !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("LeaseAt = (%v, %v), want ErrNotFound", got.ID, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("LeaseAt: %v", err)
			}
			if got.ID != tc.want {
				t.Errorf("LeaseAt resolved to %q, want %q", got.ID, tc.want)
			}
		})
	}

	if _, err := s.LeaseAt(t.Context(), "nobody@demo.test", time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an address no identity owns resolved to a lease: %v", err)
	}
}

// TestLeaseIntervalsNeverCollapseOrOverlap is the property the whole
// correlation design rests on, asserted against a real clock rather than
// a stepped one.
//
// A lease that acquires and releases inside the same millisecond used to
// describe an empty interval, because the store clock truncated to
// milliseconds and both stamps landed on the same value. An empty
// interval owns no instant, so mail that arrived while the lease was held
// resolved to no owner at all - which reads exactly like mail nobody
// leased, and would have sent phase 3 down the unbound path for a message
// with a rightful owner.
func TestLeaseIntervalsNeverCollapseOrOverlap(t *testing.T) {
	s := openTestStore(t)
	project := newProject(t, s)
	identity := newIdentity(t, s, project.ID, "tight-loop@demo.test")

	// A tight loop is the point: every iteration tries to make acquire and
	// release land inside one clock tick.
	const rounds = 200
	var previous store.Lease
	for i := range rounds {
		run, _ := newRun(t, s, project.ID)
		lease, err := acquire(t, s, run.ID, identity.ID)
		if err != nil {
			t.Fatalf("round %d: acquire: %v", i, err)
		}
		if err := s.ReleaseLease(t.Context(), lease.ID); err != nil {
			t.Fatalf("round %d: release: %v", i, err)
		}
		closed, err := s.Lease(t.Context(), lease.ID)
		if err != nil {
			t.Fatalf("round %d: read back: %v", i, err)
		}
		if !closed.ReleasedAt.After(closed.AcquiredAt) {
			t.Fatalf("round %d: lease owned nothing: acquired %s, released %s",
				i, closed.AcquiredAt, closed.ReleasedAt)
		}
		if i > 0 && closed.AcquiredAt.Before(previous.ReleasedAt) {
			t.Fatalf("round %d: interval overlaps the previous lease: acquired %s, previous released %s",
				i, closed.AcquiredAt, previous.ReleasedAt)
		}
		// Every instant the lease owned must resolve back to it.
		owner, err := s.LeaseAt(t.Context(), identity.Addr, closed.AcquiredAt)
		if err != nil {
			t.Fatalf("round %d: LeaseAt at the acquire instant: %v", i, err)
		}
		if owner.ID != closed.ID {
			t.Fatalf("round %d: the acquire instant resolved to %s, want %s", i, owner.ID, closed.ID)
		}
		previous = closed
	}
}

// TestStoreClockNeverRepeatsAnInstant states the guarantee the intervals
// above depend on, directly.
func TestStoreClockNeverRepeatsAnInstant(t *testing.T) {
	s := openTestStore(t)

	const stamps = 10_000
	seen := make(map[time.Time]struct{}, stamps)
	previous := time.Time{}
	for i := range stamps {
		now := s.Now()
		if _, repeat := seen[now]; repeat {
			t.Fatalf("stamp %d repeated %s", i, now)
		}
		if !now.After(previous) && i > 0 {
			t.Fatalf("stamp %d went backwards: %s then %s", i, previous, now)
		}
		seen[now] = struct{}{}
		previous = now
	}
}

func TestEndRunReleasesEveryLeaseItHeld(t *testing.T) {
	s := openTestStore(t)
	project := newProject(t, s)
	run, _ := newRun(t, s, project.ID)

	var leases []store.Lease
	for _, addr := range []string{"a@demo.test", "b@demo.test", "c@demo.test"} {
		identity := newIdentity(t, s, project.ID, addr)
		lease, err := acquire(t, s, run.ID, identity.ID)
		if err != nil {
			t.Fatalf("acquire %s: %v", addr, err)
		}
		leases = append(leases, lease)
	}

	if err := s.EndRun(t.Context(), run.ID, store.RunComplete, ""); err != nil {
		t.Fatalf("end run: %v", err)
	}
	for _, l := range leases {
		after, err := s.Lease(t.Context(), l.ID)
		if err != nil {
			t.Fatalf("lease: %v", err)
		}
		if after.State != store.LeaseReleased {
			t.Errorf("lease %s is %q after the run ended, want released", l.ID, after.State)
		}
	}

	ended, err := s.Run(t.Context(), run.ID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if ended.Active() || ended.EndedAt.IsZero() {
		t.Errorf("run did not end: %+v", ended)
	}

	// A finished run cannot take anything new.
	identity := newIdentity(t, s, project.ID, "late@demo.test")
	if _, err := acquire(t, s, run.ID, identity.ID); !errors.Is(err, store.ErrRunNotActive) {
		t.Errorf("acquire on an ended run = %v, want ErrRunNotActive", err)
	}
}

// TestExpireRunsIsIdempotent covers the sweeper after a crash: it runs on
// a timer and on every start, and running it twice must not undo or
// double-count anything.
func TestExpireRunsIsIdempotent(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)}
	s := openTestStoreWithClock(t, clock.Now)
	project := newProject(t, s)

	run, _ := newRun(t, s, project.ID)
	identity := newIdentity(t, s, project.ID, "expiring@demo.test")
	lease, err := acquire(t, s, run.ID, identity.ID)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	if n, err := s.ExpireRuns(t.Context()); err != nil || n != 0 {
		t.Fatalf("ExpireRuns before the deadline = (%d, %v), want (0, nil)", n, err)
	}

	clock.advance(runTTL + time.Second)
	n, err := s.ExpireRuns(t.Context())
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n != 1 {
		t.Fatalf("expired %d runs, want 1", n)
	}

	again, err := s.ExpireRuns(t.Context())
	if err != nil {
		t.Fatalf("second expire: %v", err)
	}
	if again != 0 {
		t.Errorf("a second sweep expired %d runs, want 0", again)
	}

	after, err := s.Lease(t.Context(), lease.ID)
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if after.State != store.LeaseReleased {
		t.Errorf("lease state after run expiry = %q, want released", after.State)
	}
	// The identity is usable again, which is the point of sweeping at all.
	next, _ := newRun(t, s, project.ID)
	if _, err := acquire(t, s, next.ID, identity.ID); err != nil {
		t.Errorf("acquire after the sweep: %v", err)
	}
}

// TestExpireLeasesEndsAHeldLeaseInsideALiveRun covers the lease that runs
// out before its run does.
func TestExpireLeasesEndsAHeldLeaseInsideALiveRun(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)}
	s := openTestStoreWithClock(t, clock.Now)
	project := newProject(t, s)
	run, _ := newRun(t, s, project.ID)
	identity := newIdentity(t, s, project.ID, "short@demo.test")

	lease, err := acquire(t, s, run.ID, identity.ID)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	clock.advance(leaseTTL + time.Second)

	n, err := s.ExpireLeases(t.Context())
	if err != nil {
		t.Fatalf("expire leases: %v", err)
	}
	if n != 1 {
		t.Fatalf("expired %d leases, want 1", n)
	}
	after, err := s.Lease(t.Context(), lease.ID)
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if after.State != store.LeaseExpired {
		t.Errorf("state = %q, want expired", after.State)
	}
	// The run is still active; only its lease ended.
	current, err := s.Run(t.Context(), run.ID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !current.Active() {
		t.Error("expiring a lease ended its run")
	}
}

// TestRunTokenIsNeverStoredInPlaintext is the security claim the run token
// rests on. It reads the raw column rather than trusting the accessor,
// because the accessor is the thing being checked.
func TestRunTokenIsNeverStoredInPlaintext(t *testing.T) {
	s := openTestStore(t)
	project := newProject(t, s)
	run, token := newRun(t, s, project.ID)

	if !strings.HasPrefix(token, store.RunTokenPrefix) {
		t.Errorf("token does not carry the redaction prefix")
	}

	// Every text column of the row, and every ledger detail, searched for
	// the token itself.
	values, err := s.QueryStringsForTest(t.Context(),
		`SELECT id || ' ' || project_id || ' ' || state || ' ' || fail_reason ||
			' ' || CAST(token_hash AS TEXT) FROM runs WHERE id = ?`, run.ID)
	if err != nil {
		t.Fatalf("read row: %v", err)
	}
	for _, v := range values {
		if strings.Contains(v, token) {
			t.Fatal("the raw run token is stored in the runs row")
		}
	}

	found, err := s.RunByToken(t.Context(), token)
	if err != nil {
		t.Fatalf("authenticate by token: %v", err)
	}
	if found.ID != run.ID {
		t.Errorf("token resolved to run %q, want %q", found.ID, run.ID)
	}
	if _, err := s.RunByToken(t.Context(), store.RunTokenPrefix+"wrong"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an unknown token = %v, want ErrNotFound", err)
	}
}

func TestCreateIdentityEnforcesModeInvariants(t *testing.T) {
	s := openTestStore(t)
	project := newProject(t, s)

	cases := []struct {
		name string
		in   store.NewIdentity
	}{
		{"ephemeral with a persona", store.NewIdentity{
			ProjectID: project.ID, PersonaID: "pers00000001",
			Addr: "x@demo.test", Mode: store.ModeEphemeral,
		}},
		{"pooled without a persona", store.NewIdentity{
			ProjectID: project.ID, Addr: "y@demo.test", Mode: store.ModePooled,
		}},
		{"unknown mode", store.NewIdentity{
			ProjectID: project.ID, Addr: "z@demo.test", Mode: "borrowed",
		}},
		{"no address", store.NewIdentity{
			ProjectID: project.ID, Mode: store.ModeEphemeral,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.CreateIdentity(t.Context(), tc.in); err == nil {
				t.Error("accepted an identity that breaks a mode invariant")
			}
		})
	}
}

// TestIdentityAddressIsCanonical keeps the lookup a sender's spelling can
// reach: an envelope carries whatever the application wrote.
func TestIdentityAddressIsCanonical(t *testing.T) {
	s := openTestStore(t)
	project := newProject(t, s)
	created := newIdentity(t, s, project.ID, "Mixed.Case@DEMO.TEST")

	found, err := s.IdentityByAddr(t.Context(), "mixed.case@demo.test")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if found.ID != created.ID {
		t.Errorf("lookup resolved to %q, want %q", found.ID, created.ID)
	}
}

// testClock is a settable clock for the sweeper tests, which are about
// deadlines and would otherwise need real time to pass.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// TestPooledSuspicionUsesTheResolutionAMessageCanState is the boundary the
// pooled handover guard turns on.
//
// A message states when it was generated to the second; a lease is
// acquired at nanosecond resolution. Comparing the two raw would mark a
// run's own mail as older than its own lease whenever the acquire landed
// mid-second, which would refuse every code on a correct application. The
// comparison therefore truncates the lease instant down to the second the
// message could have named, and that is exactly one second of slack - no
// more, which is why the mail from a second earlier is still refused.
func TestPooledSuspicionUsesTheResolutionAMessageCanState(t *testing.T) {
	s := openTestStore(t)
	project := newProject(t, s)

	persona, err := s.CreatePersona(t.Context(), store.Persona{
		ProjectID: project.ID, Name: "pooled", Email: "pooled-guard@demo.test",
		PasswordEnc: []byte("sealed"), Role: "pro",
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := s.CreateIdentity(t.Context(), store.NewIdentity{
		ProjectID: project.ID, PersonaID: persona.ID,
		Addr: "pooled-guard@demo.test", Role: "pro", Mode: store.ModePooled,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := newRun(t, s, project.ID)
	lease, err := acquire(t, s, run.ID, identity.ID)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	second := lease.AcquiredAt.Truncate(time.Second)

	cases := []struct {
		name   string
		origin time.Time
		want   string
	}{
		{"generated after the acquire", lease.AcquiredAt.Add(time.Millisecond), store.SuspectNone},
		{"generated in the acquire's own second", second, store.SuspectNone},
		{"generated the second before", second.Add(-time.Second), store.SuspectPredatesLease},
		{"generated while the previous run held it", second.Add(-time.Minute), store.SuspectPredatesLease},
		{"unable to say", time.Time{}, store.SuspectOriginUnknown},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg, err := s.InsertMessage(t.Context(), store.Message{
				ProjectID:  project.ID,
				Subject:    tc.name,
				TextBody:   "code 123456",
				ReceivedAt: lease.AcquiredAt.Add(time.Duration(i+1) * time.Second),
				Recipients: []store.Recipient{
					{Addr: identity.Addr, Kind: store.RecipientEnvelope},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			var bound []store.Binding
			if err := s.WithTx(t.Context(), func(tx *store.Tx) error {
				var err error
				bound, _, err = tx.BindRecipients(t.Context(), msg.ID,
					[]string{identity.Addr}, msg.ReceivedAt, tc.origin)
				return err
			}); err != nil {
				t.Fatalf("bind: %v", err)
			}
			if len(bound) != 1 {
				t.Fatalf("bound %d leases, want 1", len(bound))
			}
			if bound[0].Suspect != tc.want {
				t.Errorf("suspect = %q, want %q", bound[0].Suspect, tc.want)
			}
		})
	}
}
