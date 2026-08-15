package personas_test

import (
	"testing"
)

// TestEphemeralAddressesAreNeverReused pins the invariant the README's
// exclusivity sentence rests on.
//
// The claim is that mail sent to an ephemeral address can only ever belong
// to the run that address was minted for. That holds not because the
// correlation query is careful but because there is nothing for it to get
// wrong: reserveEphemeral mints a fresh random local part and creates a
// fresh identity in the same commit as the lease (personas/lease.go:398,
// personas/lease.go:601), and identities.addr is UNIQUE
// (store/schema_v3.sql:52). One address therefore has exactly one lease for
// its whole life, so the "which lease owned this address at this instant"
// lookup can return that one lease or nothing, never somebody else's.
//
// It is worth a test because the invariant is spread across three files and
// none of them says out loud that a public promise depends on it. A change
// that pooled ephemeral addresses, or that re-leased a released ephemeral
// identity, would keep every existing test green and quietly turn that
// promise into a bug.
func TestEphemeralAddressesAreNeverReused(t *testing.T) {
	const runs = 32
	w := newWedge(t)
	ctx := t.Context()

	// Every address seen so far, against the lease that held it. Release
	// happens inside the loop, so each acquire after the first is made
	// while previously used addresses are sitting freed and available to
	// anything that might try to hand one out again.
	seen := make(map[string]string, runs)

	for range runs {
		r, grant := w.run(ctx, "pro")

		if prior, reused := seen[grant.Addr]; reused {
			t.Fatalf("address %s was handed to lease %s and again to lease %s",
				grant.Addr, prior, grant.LeaseID)
		}
		seen[grant.Addr] = grant.LeaseID

		// The invariant stated in the terms the correlation query uses:
		// asked who held this address at the instant it was acquired, the
		// store must name this lease and no other.
		lease, err := w.store.Lease(ctx, grant.LeaseID)
		if err != nil {
			t.Fatalf("read lease %s: %v", grant.LeaseID, err)
		}
		owner, err := w.store.LeaseAt(ctx, grant.Addr, lease.AcquiredAt)
		if err != nil {
			t.Fatalf("LeaseAt(%s, acquired_at): %v", grant.Addr, err)
		}
		if owner.ID != grant.LeaseID {
			t.Fatalf("LeaseAt(%s, acquired_at) = lease %s, want the lease that minted it, %s",
				grant.Addr, owner.ID, grant.LeaseID)
		}

		if err := w.svc.Release(ctx, r.ID, grant.LeaseID); err != nil {
			t.Fatalf("release lease %s: %v", grant.LeaseID, err)
		}
	}

	if len(seen) != runs {
		t.Fatalf("%d acquires produced %d distinct addresses", runs, len(seen))
	}
}
