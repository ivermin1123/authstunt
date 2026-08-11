package store_test

import (
	"errors"
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
