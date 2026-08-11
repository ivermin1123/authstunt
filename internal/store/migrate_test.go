package store_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ivermin1123/authstunt/internal/secrets"
	"github.com/ivermin1123/authstunt/internal/store"
)

// seedV1 builds a database as the v1 binary left it and returns its
// directory plus the key it was sealed with. The rows go in through raw
// v1 statements on purpose: the typed methods now write the v2 column
// shape, so using them would test the migration against a database no v1
// binary could have produced.
func seedV1(t *testing.T) (dir string, key *secrets.Key) {
	t.Helper()
	ctx := t.Context()
	dir = t.TempDir()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "keys"), "test")
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.OpenAtSchemaVersionForTest(ctx, dir, key, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := old.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	sealed, err := key.Seal([]byte("your code is 483920"))
	if err != nil {
		t.Fatal(err)
	}
	sealedExtraction, err := key.Seal([]byte(`{"otp":"483920"}`))
	if err != nil {
		t.Fatal(err)
	}

	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO projects (id, name, created_at) VALUES (?, ?, ?)`,
			[]any{"proj0000000a", "legacy", "2026-08-01T00:00:00.000000000Z"}},
		{`INSERT INTO personas (id, project_id, name, email, password_enc, role,
			traits_json, seed_state, seed_output_ref, created_at, updated_at)
		  VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)`,
			[]any{"pers0000000a", "proj0000000a", "alice", "alice@demo.test",
				[]byte("sealed"), "free", "{}", store.SeedSeeded,
				"2026-08-01T00:00:00.000000000Z", "2026-08-01T00:00:00.000000000Z"}},
		// The extracted row and the panicked row, which is the pair the
		// backfill has to tell apart.
		{`INSERT INTO messages (id, project_id, from_addr, subject, channel, raw_ref,
			html_ref, text_body, extracted_json, quarantined, received_at)
		  VALUES (?, ?, ?, ?, 'email', NULL, NULL, ?, ?, 0, ?)`,
			[]any{"msg00000000a", "proj0000000a", "app@demo.test", "extracted",
				sealed, sealedExtraction, "2026-08-01T00:00:01.000000000Z"}},
		{`INSERT INTO messages (id, project_id, from_addr, subject, channel, raw_ref,
			html_ref, text_body, extracted_json, quarantined, received_at)
		  VALUES (?, ?, ?, ?, 'email', NULL, NULL, ?, NULL, 0, ?)`,
			[]any{"msg00000000b", "proj0000000a", "app@demo.test", "panicked",
				sealed, "2026-08-01T00:00:02.000000000Z"}},
		{`INSERT INTO message_recipients (message_id, addr, kind) VALUES (?, ?, ?)`,
			[]any{"msg00000000a", "alice@demo.test", store.RecipientEnvelope}},
		{`INSERT INTO allowlist (project_id, pattern, ord) VALUES (?, ?, 0)`,
			[]any{"proj0000000a", "demo.test"}},
	}
	for _, s := range statements {
		if err := old.ExecForTest(ctx, s.query, s.args...); err != nil {
			t.Fatalf("seed v1: %v", err)
		}
	}
	for i, action := range []string{"first", "second", "third"} {
		err := old.ExecForTest(ctx,
			`INSERT INTO ledger (project_id, ts, actor, run_id, persona_id, action, detail_json)
			 VALUES (?, ?, 'system', 'run1', ?, ?, '{}')`,
			"proj0000000a", fmt.Sprintf("2026-08-01T00:00:%02d.000000000Z", i+1),
			"pers0000000a", action)
		if err != nil {
			t.Fatalf("seed ledger: %v", err)
		}
	}
	return dir, key
}

// upgrade reopens a seeded directory through the real Open, which runs
// every pending migration.
func upgrade(t *testing.T, dir string, key *secrets.Key) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), dir, key, store.Options{})
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return s
}

func TestMigrateV1ToV2PreservesLedgerAndNeverReusesIDs(t *testing.T) {
	ctx := t.Context()
	dir, key := seedV1(t)
	s := upgrade(t, dir, key)

	pragmas, err := s.ReadPragmas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pragmas.UserVersion != store.SchemaVersion {
		t.Fatalf("user_version = %d after upgrade, want %d", pragmas.UserVersion, store.SchemaVersion)
	}

	entries, err := s.ListLedger(ctx, store.LedgerFilter{ProjectID: "proj0000000a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("ledger holds %d entries after upgrade, want 3", len(entries))
	}
	for i, want := range []string{"first", "second", "third"} {
		if entries[i].ID != int64(i+1) || entries[i].Action != want {
			t.Errorf("entry %d = (id %d, %q), want (id %d, %q)",
				i, entries[i].ID, entries[i].Action, i+1, want)
		}
		if entries[i].RunID != "run1" || entries[i].PersonaID != "pers0000000a" {
			t.Errorf("entry %d lost its run or persona: %+v", i, entries[i])
		}
	}

	// The rebuild must not have dropped the ledger indexes, and the
	// persona one is partial: an index recreated without its WHERE clause
	// would still be present under the same name.
	indexes, err := s.QueryStringsForTest(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'ledger' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ledger_persona_ts", "ledger_project_ts", "ledger_run"} {
		if !slices.Contains(indexes, want) {
			t.Errorf("index %s is gone after the rebuild; have %v", want, indexes)
		}
	}
	partial, err := s.QueryStringsForTest(ctx,
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'ledger_persona_ts'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(partial) != 1 || !strings.Contains(partial[0], "WHERE persona_id IS NOT NULL") {
		t.Errorf("ledger_persona_ts is no longer partial: %v", partial)
	}

	// Foreign keys elsewhere in the schema survive the rebuild.
	if _, err := s.CreatePersona(ctx, store.Persona{
		ProjectID: "does-not-exist", Name: "orphan",
		Email: "orphan@demo.test", PasswordEnc: []byte("sealed"),
	}); err == nil {
		t.Error("foreign keys stopped being enforced after the migration")
	}

	// The point of AUTOINCREMENT: deleting the highest row must not free
	// its id for the next insert.
	if err := s.ExecForTest(ctx, `DELETE FROM ledger WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	id, err := s.AppendLedger(ctx, store.LedgerEntry{
		ProjectID: "proj0000000a", Actor: store.ActorSystem, Action: "after-delete",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id <= 3 {
		t.Fatalf("new ledger id = %d, want above every id ever used (3)", id)
	}
}

func TestMigrateV1ToV2BackfillsExtractionState(t *testing.T) {
	ctx := t.Context()
	dir, key := seedV1(t)
	s := upgrade(t, dir, key)

	extracted, err := s.Message(ctx, "msg00000000a")
	if err != nil {
		t.Fatal(err)
	}
	if extracted.ExtractionState != store.ExtractionSuccess {
		t.Errorf("row with an extraction backfilled as %q, want %q",
			extracted.ExtractionState, store.ExtractionSuccess)
	}
	if extracted.ExtractedJSON != `{"otp":"483920"}` {
		t.Errorf("extraction lost in the migration: %q", extracted.ExtractedJSON)
	}

	panicked, err := s.Message(ctx, "msg00000000b")
	if err != nil {
		t.Fatal(err)
	}
	if panicked.ExtractionState != store.ExtractionFailed {
		t.Errorf("row with a NULL extraction backfilled as %q, want %q",
			panicked.ExtractionState, store.ExtractionFailed)
	}

	// Neither is pending, so startup recovery never rescans mail that
	// predates the migration.
	pending, err := s.ListPendingExtractions(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("recovery would rescan %d migrated rows, want 0", len(pending))
	}

	// The rest of the seeded data is still there and still readable.
	if body := extracted.TextBody; body != "your code is 483920" {
		t.Errorf("body = %q after upgrade", body)
	}
	if len(extracted.Recipients) != 1 {
		t.Errorf("recipients lost in the migration: %+v", extracted.Recipients)
	}
	patterns, err := s.Allowlist(ctx, "proj0000000a")
	if err != nil {
		t.Fatal(err)
	}
	if len(patterns) != 1 || patterns[0] != "demo.test" {
		t.Errorf("allowlist = %v after upgrade", patterns)
	}
}

func TestNextEventGenerationPersistsAndIncrements(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "keys"), "test")
	if err != nil {
		t.Fatal(err)
	}

	first := openAt(t, dir, key)
	one, err := first.NextEventGeneration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	two, err := first.NextEventGeneration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if one != 1 || two != 2 {
		t.Fatalf("generations = %d, %d; want 1, 2", one, two)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	// The restart is the whole point: a counter that reset here would
	// reissue event ids a reconnecting client has already seen.
	second := openAt(t, dir, key)
	three, err := second.NextEventGeneration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if three != 3 {
		t.Fatalf("generation after restart = %d, want 3", three)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

// openAt opens a store the caller closes itself, for the tests that need
// to prove something survives a restart.
func openAt(t *testing.T, dir string, key *secrets.Key) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), dir, key, store.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s
}
