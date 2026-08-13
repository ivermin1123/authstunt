package store_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ivermin1123/authstunt/internal/store"
)

func TestSingletonProjectAccessor(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)

	// Zero: an uninitialized data directory. serve turns this into the
	// bootstrap path, so it has to be distinguishable from a real failure.
	if _, err := s.SingleProject(ctx); !errors.Is(err, store.ErrNoProject) {
		t.Fatalf("empty database returned %v, want ErrNoProject", err)
	}

	only, err := s.CreateProject(ctx, "the-one")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.SingleProject(ctx)
	if err != nil {
		t.Fatalf("one project returned an error: %v", err)
	}
	if got.ID != only.ID || got.Name != only.Name {
		t.Errorf("SingleProject = %+v, want %+v", got, only)
	}

	// Many: a process serves one project and one key, so picking one here
	// would seal mail under a key the reader does not have. It is an error,
	// not a choice.
	if _, err := s.CreateProject(ctx, "the-other"); err != nil {
		t.Fatal(err)
	}
	_, err = s.SingleProject(ctx)
	if !errors.Is(err, store.ErrMultipleProjects) {
		t.Fatalf("two projects returned %v, want ErrMultipleProjects", err)
	}
}

func TestAllowlistStoresCanonicalPatterns(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)

	err := s.SetAllowlist(ctx, project.ID, []string{"Demo.Test", "*.Démo.Test"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Allowlist(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"demo.test", "*.xn--dmo-bma.test"}
	if len(got) != len(want) {
		t.Fatalf("allowlist = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("allowlist[%d] = %q, want %q (declaration order is load bearing)", i, got[i], want[i])
		}
	}

	// A pattern that cannot be canonicalized is refused at configuration
	// time. Storing it would leave an entry that silently matches nothing
	// at delivery time, which reads as "the allowlist is wrong" only after
	// mail has already been quarantined.
	if err := s.SetAllowlist(ctx, project.ID, []string{"demo.test", "mail_server.test"}); err == nil {
		t.Fatal("an unusable pattern was accepted")
	}
	after, err := s.Allowlist(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(want) {
		t.Fatalf("a rejected update changed the stored allowlist: %v", after)
	}
}

func TestProjectBearerIsReturnedOnceAndNeverStoredRaw(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)

	project, err := s.CreateProject(ctx, "bearer-owner")
	if err != nil {
		t.Fatal(err)
	}

	// A project starts unprovisioned, and that is distinguishable from a
	// project that does not exist.
	has, err := s.ProjectHasBearer(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("a fresh project already reports a bearer")
	}
	if _, err := s.ProjectHasBearer(ctx, "no-such-project"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown project returned %v, want ErrNotFound", err)
	}

	token, err := s.SetProjectBearer(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, store.ProjectBearerPrefix) {
		t.Fatalf("bearer %q does not carry the redaction prefix", store.ProjectBearerPrefix)
	}

	// The raw value authenticates, and it is the only thing that does.
	got, err := s.ProjectByBearer(ctx, token)
	if err != nil {
		t.Fatalf("the minted bearer did not authenticate: %v", err)
	}
	if got.ID != project.ID {
		t.Fatalf("bearer resolved to %q, want %q", got.ID, project.ID)
	}

	// The row keeps a digest, never the token. Reading every column of
	// every project back as text must not turn up the secret anywhere.
	columns, err := s.QueryStringsForTest(ctx,
		`SELECT CAST(id AS TEXT) || ' ' || CAST(name AS TEXT) || ' ' ||
		        COALESCE(HEX(bearer_hash), '') FROM projects`)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range columns {
		if strings.Contains(row, token) {
			t.Fatal("the raw bearer is readable from the projects table")
		}
	}
}

func TestProjectBearerRotationInvalidatesThePreviousValue(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)

	project, err := s.CreateProject(ctx, "rotator")
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.SetProjectBearer(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.SetProjectBearer(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("rotation returned the same token twice")
	}

	// Rotation is immediate: there is never a window with two live
	// credentials for one project.
	if _, err := s.ProjectByBearer(ctx, first); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the rotated-out bearer still authenticates: %v", err)
	}
	if _, err := s.ProjectByBearer(ctx, second); err != nil {
		t.Fatalf("the current bearer does not authenticate: %v", err)
	}

	// Revoke leaves the project unprovisioned rather than deleting it.
	if err := s.RevokeProjectBearer(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectByBearer(ctx, second); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a revoked bearer still authenticates: %v", err)
	}
	if _, err := s.Project(ctx, project.ID); err != nil {
		t.Fatalf("revoking the bearer damaged the project: %v", err)
	}
	// Revoking twice is not an error: the caller asked for a state.
	if err := s.RevokeProjectBearer(ctx, project.ID); err != nil {
		t.Fatalf("second revoke: %v", err)
	}
}

func TestProjectBearerDoesNotLeakAcrossProjectsOrMatchNothing(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)

	one, err := s.CreateProject(ctx, "one")
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.CreateProject(ctx, "two")
	if err != nil {
		t.Fatal(err)
	}
	oneToken, err := s.SetProjectBearer(ctx, one.ID)
	if err != nil {
		t.Fatal(err)
	}
	twoToken, err := s.SetProjectBearer(ctx, two.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Each bearer resolves to its own project and to no other.
	for token, want := range map[string]string{oneToken: one.ID, twoToken: two.ID} {
		got, err := s.ProjectByBearer(ctx, token)
		if err != nil {
			t.Fatalf("bearer did not authenticate: %v", err)
		}
		if got.ID != want {
			t.Fatalf("bearer resolved to %q, want %q", got.ID, want)
		}
	}

	// The empty string is the value an unset header arrives as. It must
	// never match the NULL an unprovisioned project holds.
	unprovisioned, err := s.CreateProject(ctx, "unprovisioned")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectByBearer(ctx, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the empty bearer returned %v, want ErrNotFound", err)
	}
	if _, err := s.ProjectByBearer(ctx, store.ProjectBearerPrefix+"not-a-real-token"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an unknown bearer returned %v, want ErrNotFound", err)
	}
	if has, err := s.ProjectHasBearer(ctx, unprovisioned.ID); err != nil || has {
		t.Fatalf("unprovisioned project: has = %v, err = %v", has, err)
	}

	// Setting a bearer on a project that does not exist is ErrNotFound,
	// not a silently created row.
	if _, err := s.SetProjectBearer(ctx, "no-such-project"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("set bearer on an unknown project returned %v, want ErrNotFound", err)
	}
}
