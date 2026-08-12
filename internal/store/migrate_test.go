package store_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

// TestMigrateToV3AddsTheLeaseTablesOnAPopulatedDatabase upgrades a
// database that already carries v1 rows, because a migration only tested
// on an empty file is a migration tested on the case that cannot fail.
func TestMigrateToV3AddsTheLeaseTablesOnAPopulatedDatabase(t *testing.T) {
	ctx := t.Context()
	dir, key := seedV1(t)
	s := upgrade(t, dir, key)

	pragmas, err := s.ReadPragmas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Asserted against the constant rather than a literal: an upgrade runs
	// every migration, so pinning the number here would mean editing this
	// test at every future one, which is how a version check turns into a
	// line nobody reads.
	if pragmas.UserVersion != store.SchemaVersion {
		t.Fatalf("user_version = %d, want %d", pragmas.UserVersion, store.SchemaVersion)
	}

	// The v1 rows are still there. A migration that quietly dropped the
	// data it was migrating would still pass a schema check.
	names, err := s.QueryStringsForTest(ctx, `SELECT name FROM projects`)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "legacy" {
		t.Errorf("projects after upgrade = %v, want the v1 row", names)
	}

	// The new tables work end to end on the upgraded database, which is a
	// stronger claim than their presence in sqlite_master.
	run, _, err := s.CreateRun(ctx, store.NewRun{ProjectID: "proj0000000a", TTL: time.Minute})
	if err != nil {
		t.Fatalf("create run on an upgraded database: %v", err)
	}
	identity, err := s.CreateIdentity(ctx, store.NewIdentity{
		ProjectID: "proj0000000a",
		Addr:      "upgraded@demo.test",
		Mode:      store.ModeEphemeral,
	})
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}
	err = s.WithTx(ctx, func(tx *store.Tx) error {
		_, err := tx.AcquireLease(ctx, store.NewLease{
			RunID: run.ID, IdentityID: identity.ID, TTL: time.Minute,
		})
		return err
	})
	if err != nil {
		t.Fatalf("acquire on an upgraded database: %v", err)
	}
}

// TestMigrateToV4OnAPopulatedV3Database upgrades a database that already
// carries a run, an identity and a held lease, because v4 adds a column
// to `leases` and a table referencing rows that are already there.
func TestMigrateToV4OnAPopulatedV3Database(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "keys"), "test")
	if err != nil {
		t.Fatal(err)
	}

	var (
		projectID  = "proj0000000v"
		runID      string
		identityID string
		leaseID    string
	)
	func() {
		old, err := store.OpenAtSchemaVersionForTest(ctx, dir, key, 3)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
		}()
		if err := old.ExecForTest(ctx,
			`INSERT INTO projects (id, name, created_at) VALUES (?, 'v3', ?)`,
			projectID, "2026-08-12T00:00:00.000000000Z"); err != nil {
			t.Fatal(err)
		}
		run, _, err := old.CreateRun(ctx, store.NewRun{ProjectID: projectID, TTL: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		runID = run.ID
		identity, err := old.CreateIdentity(ctx, store.NewIdentity{
			ProjectID: projectID,
			Addr:      "already-here@demo.test",
			Mode:      store.ModeEphemeral,
		})
		if err != nil {
			t.Fatal(err)
		}
		identityID = identity.ID
		// The lease goes in as raw SQL in the v3 column shape. The typed
		// insert belongs to this binary, which already knows about the v4
		// column, and a test that seeded through it would be upgrading a
		// database no older binary could have written.
		leaseID = "lease0000v3a"
		if err := old.ExecForTest(ctx,
			`INSERT INTO leases (id, run_id, identity_id, role, state, seed_state,
				seed_fingerprint, acquired_at, expires_at, released_at)
			 VALUES (?, ?, ?, 'pro', 'held', 'seeded', '', ?, ?, NULL)`,
			leaseID, runID, identityID,
			"2026-08-12T10:00:00.000000000Z", "2026-08-12T11:00:00.000000000Z"); err != nil {
			t.Fatal(err)
		}
	}()

	s := upgrade(t, dir, key)

	pragmas, err := s.ReadPragmas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pragmas.UserVersion != store.SchemaVersion {
		t.Fatalf("user_version = %d, want %d", pragmas.UserVersion, store.SchemaVersion)
	}

	// The lease written before v4 existed reads back with the added
	// column at its default, rather than as a row the scanner refuses.
	lease, err := s.Lease(ctx, leaseID)
	if err != nil {
		t.Fatalf("read a pre-v4 lease: %v", err)
	}
	if lease.InCooldown {
		t.Error("a lease acquired before v4 came back marked as acquired in cooldown")
	}
	if !lease.Held() {
		t.Errorf("the pre-v4 lease is %s, want held", lease.State)
	}

	// The new tables work end to end against those pre-existing rows.
	msg, err := s.InsertMessage(ctx, store.Message{
		ProjectID:  projectID,
		Subject:    "after the upgrade",
		TextBody:   "code 123456",
		ReceivedAt: lease.AcquiredAt.Add(time.Second),
		Recipients: []store.Recipient{
			{Addr: "already-here@demo.test", Kind: store.RecipientEnvelope},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WithTx(ctx, func(tx *store.Tx) error {
		bound, unbound, err := tx.BindRecipients(ctx, msg.ID,
			[]string{"already-here@demo.test"}, msg.ReceivedAt, msg.ReceivedAt)
		if err != nil {
			return err
		}
		if len(bound) != 1 || len(unbound) != 0 {
			t.Errorf("bound %d, unbound %d, want 1 and 0", len(bound), len(unbound))
		}
		return nil
	}); err != nil {
		t.Fatalf("bind on an upgraded database: %v", err)
	}
	if _, err := s.RecordClaim(ctx, store.NewClaim{
		LeaseID: leaseID, RunID: runID, Kind: store.ClaimEmailOTP,
		MessageID: msg.ID, IdempotencyKey: "after-upgrade", TTL: time.Minute,
	}); err != nil {
		t.Fatalf("claim on an upgraded database: %v", err)
	}
}

// TestV3IndexesSurviveTheMigration checks the partial indexes by name.
// Exclusivity is the partial index; an index recreated without its WHERE
// clause would either be wrong or reject every second lease on any
// identity, and both are silent until a user hits them.
func TestV3IndexesSurviveTheMigration(t *testing.T) {
	dir, key := seedV1(t)
	s := upgrade(t, dir, key)

	sqls, err := s.QueryStringsForTest(t.Context(),
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`,
		"leases_one_held_per_identity")
	if err != nil {
		t.Fatal(err)
	}
	if len(sqls) != 1 {
		t.Fatalf("the exclusivity index is missing after the upgrade")
	}
	if !strings.Contains(sqls[0], "WHERE state = 'held'") {
		t.Errorf("the exclusivity index lost its WHERE clause: %s", sqls[0])
	}
	if !strings.Contains(strings.ToUpper(sqls[0]), "UNIQUE") {
		t.Errorf("the exclusivity index is not unique: %s", sqls[0])
	}
}

// TestPersonaWithAPooledIdentityCannotBePruned pins the owner's amendment:
// identities.persona_id carries no cascade, so deleting a persona an
// identity still points at fails instead of taking the lease history with
// it.
func TestPersonaWithAPooledIdentityCannotBePruned(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)

	persona, err := s.CreatePersona(ctx, store.Persona{
		ProjectID:   project.ID,
		Name:        "pooled-pro",
		Email:       "pooled-pro@demo.test",
		PasswordEnc: []byte("sealed"),
		Role:        "pro",
	})
	if err != nil {
		t.Fatalf("create persona: %v", err)
	}
	if _, err := s.CreateIdentity(ctx, store.NewIdentity{
		ProjectID: project.ID,
		PersonaID: persona.ID,
		Addr:      "pooled-pro@demo.test",
		Role:      "pro",
		Mode:      store.ModePooled,
	}); err != nil {
		t.Fatalf("create identity: %v", err)
	}

	if err := s.DeletePersonaForTest(ctx, persona.ID); err == nil {
		t.Fatal("a persona backing a pooled identity was deleted, taking its lease history with it")
	}
}

// TestMigrateToV5OnAPopulatedV4Database upgrades a database that already
// holds bindings, because v5 rebuilds `message_bindings` to widen the
// constraint on `suspect` and a rebuild that lost rows would erase the
// record of who owned which mail.
func TestMigrateToV5OnAPopulatedV4Database(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "keys"), "test")
	if err != nil {
		t.Fatal(err)
	}

	const (
		projectID  = "proj0000000w"
		identityID = "iden000000v4"
		runID      = "run00000000v4"
		leaseID    = "lease0000v4a"
		messageID  = "msg0000000v4"
		acquiredAt = "2026-08-12T10:00:00.000000000Z"
	)
	func() {
		old, err := store.OpenAtSchemaVersionForTest(ctx, dir, key, 4)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
		}()
		// Every row goes in as raw SQL in the v4 shape. Seeding through
		// this binary's typed writers would build a database no older
		// binary could have written, which is not the thing being upgraded.
		stmts := []struct {
			query string
			args  []any
		}{
			{`INSERT INTO projects (id, name, created_at) VALUES (?, 'v4', ?)`,
				[]any{projectID, acquiredAt}},
			{`INSERT INTO runs (id, project_id, token_hash, state, fail_reason,
				checkpoint_at, started_at, expires_at, ended_at)
			  VALUES (?, ?, X'00', 'active', '', ?, ?, '2026-08-12T11:00:00.000000000Z', NULL)`,
				[]any{runID, projectID, acquiredAt, acquiredAt}},
			{`INSERT INTO identities (id, project_id, persona_id, addr, role, mode,
				cooldown_until, created_at)
			  VALUES (?, ?, NULL, 'bound-before-v5@demo.test', 'pro', 'ephemeral', NULL, ?)`,
				[]any{identityID, projectID, acquiredAt}},
			{`INSERT INTO leases (id, run_id, identity_id, role, state, seed_state,
				seed_fingerprint, acquired_at, expires_at, released_at, acquired_in_cooldown)
			  VALUES (?, ?, ?, 'pro', 'held', 'seeded', '', ?, '2026-08-12T11:00:00.000000000Z', NULL, 0)`,
				[]any{leaseID, runID, identityID, acquiredAt}},
			{`INSERT INTO messages (id, project_id, from_addr, subject, channel, raw_ref,
				html_ref, text_body, extracted_json, quarantined, received_at, extraction_state)
			  VALUES (?, ?, 'app@acme.example', 'before v5', 'email', '', '', ?, NULL, 0, ?, 'failed')`,
				[]any{messageID, projectID, []byte("sealed by the v4 binary"), acquiredAt}},
			{`INSERT INTO message_bindings (message_id, lease_id, run_id, identity_id,
				addr, bound_at, suspect)
			  VALUES (?, ?, ?, ?, 'bound-before-v5@demo.test', ?, 'after_release')`,
				[]any{messageID, leaseID, runID, identityID, acquiredAt}},
		}
		for _, s := range stmts {
			if err := old.ExecForTest(ctx, s.query, s.args...); err != nil {
				t.Fatalf("seed v4: %v", err)
			}
		}
	}()

	s := upgrade(t, dir, key)

	pragmas, err := s.ReadPragmas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pragmas.UserVersion != store.SchemaVersion {
		t.Fatalf("user_version = %d, want %d", pragmas.UserVersion, store.SchemaVersion)
	}

	// The pre-v5 binding survived the rebuild with its suspicion intact.
	bindings, err := s.ListLeaseBindings(ctx, leaseID)
	if err != nil {
		t.Fatalf("read a pre-v5 binding: %v", err)
	}
	if len(bindings) != 1 {
		t.Fatalf("the rebuilt table holds %d bindings, want the one written at v4", len(bindings))
	}
	if bindings[0].Suspect != store.SuspectAfterRelease {
		t.Errorf("suspect = %q, want it carried through the rebuild", bindings[0].Suspect)
	}
	if bindings[0].MessageID != messageID || bindings[0].RunID != runID {
		t.Errorf("the rebuilt row lost its keys: %+v", bindings[0])
	}

	// And the widened constraint accepts the values v5 exists to record.
	for _, suspect := range []string{store.SuspectPredatesLease, store.SuspectOriginUnknown} {
		if err := s.ExecForTest(ctx,
			`INSERT INTO message_bindings (message_id, lease_id, run_id, identity_id,
				addr, bound_at, suspect)
			 VALUES (?, ?, ?, ?, 'bound-before-v5@demo.test', ?, ?)`,
			messageID, leaseID+suspect, runID, identityID, acquiredAt, suspect); err == nil {
			t.Errorf("a binding on a lease that does not exist was accepted for %q", suspect)
		}
	}
	if err := s.ExecForTest(ctx,
		`UPDATE message_bindings SET suspect = ? WHERE message_id = ?`,
		store.SuspectPredatesLease, messageID); err != nil {
		t.Errorf("the widened constraint refused %q: %v", store.SuspectPredatesLease, err)
	}
	if err := s.ExecForTest(ctx,
		`UPDATE message_bindings SET suspect = 'invented' WHERE message_id = ?`,
		messageID); err == nil {
		t.Error("the rebuilt table accepted a suspicion value outside the vocabulary")
	}
}
