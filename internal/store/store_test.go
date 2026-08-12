package store_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivermin1123/authstunt/internal/secrets"
	"github.com/ivermin1123/authstunt/internal/store"
)

// skipOnWindows guards assertions on POSIX mode bits. Windows derives the
// mode Stat reports from the file attributes rather than from an ACL, so it
// answers 0666 for a file created 0600 and 0777 for a directory created
// 0700; the mode there says nothing about who can read the path (design
// item 14). Confidentiality on Windows rests on the key file DACL, which
// internal/secrets asserts directly.
func skipOnWindows(t *testing.T, why string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip(why)
	}
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, _ := openTestStoreWithLogs(t)
	return s
}

// openTestStoreWithLogs also hands back the store's log output, for the
// tests that assert a read path reported something it could not fix.
// Keeping every test store on its own logger also keeps the expected
// warnings out of the test run's output.
func openTestStoreWithLogs(t *testing.T) (*store.Store, *logBuffer) {
	t.Helper()
	dir := t.TempDir()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "keys"), "test")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	logs := &logBuffer{}
	s, err := store.Open(t.Context(), dir, key, store.Options{
		Logger: slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return s, logs
}

// steppedClock returns a clock that advances by step on every read,
// starting at start.
//
// It exists for the tests that assert on the width of an interval rather
// than on its ordering. The store clock guarantees two stamps are never
// equal, but under a real clock consecutive stamps can be one nanosecond
// apart, and "a moment inside this interval" is then not addressable.
func steppedClock(start time.Time, step time.Duration) func() time.Time {
	var mu sync.Mutex
	current := start.Add(-step)
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		current = current.Add(step)
		return current
	}
}

// openTestStoreWithClock is openTestStore with the clock injected, for
// the tests about deadlines: a sweeper test that waited for real time to
// pass would be slow and flaky in exchange for proving nothing extra.
func openTestStoreWithClock(t *testing.T, now func() time.Time) *store.Store {
	t.Helper()
	dir := t.TempDir()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "keys"), "test")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	s, err := store.Open(t.Context(), dir, key, store.Options{
		Now:    now,
		Logger: slog.New(slog.NewTextHandler(&logBuffer{}, nil)),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return s
}

// logBuffer collects log output. Reads run concurrently in some tests, so
// the writes are serialized.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newProject(t *testing.T, s *store.Store) store.Project {
	t.Helper()
	p, err := s.CreateProject(t.Context(), "proj-"+store.NewID())
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p
}

func newPersona(t *testing.T, s *store.Store, projectID, name string) store.Persona {
	t.Helper()
	p, err := s.CreatePersona(t.Context(), store.Persona{
		ProjectID:   projectID,
		Name:        name,
		Email:       name + "-" + store.NewID() + "@demo.test",
		PasswordEnc: []byte("sealed"),
		Role:        "free",
	})
	if err != nil {
		t.Fatalf("create persona: %v", err)
	}
	return p
}

func TestPragmasActive(t *testing.T) {
	s := openTestStore(t)
	p, err := s.ReadPragmas(t.Context())
	if err != nil {
		t.Fatalf("read pragmas: %v", err)
	}
	if !p.IsWAL {
		t.Errorf("journal_mode = %q, want wal", p.JournalMode)
	}
	if !p.IsSyncNormal {
		t.Errorf("synchronous = %d, want 1 (normal)", p.Synchronous)
	}
	if !p.ForeignKeys {
		t.Error("foreign_keys is off")
	}
	if p.BusyTimeout != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", p.BusyTimeout)
	}
	if p.UserVersion != store.SchemaVersion {
		t.Errorf("user_version = %d, want %d", p.UserVersion, store.SchemaVersion)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	s := openTestStore(t)
	_, err := s.CreatePersona(t.Context(), store.Persona{
		ProjectID:   "does-not-exist",
		Name:        "orphan",
		Email:       "orphan@demo.test",
		PasswordEnc: []byte("sealed"),
	})
	if err == nil {
		t.Fatal("persona with a dangling project_id was accepted")
	}
}

func TestMigrationIsIdempotentAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "keys"), "test")
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Open(t.Context(), dir, key, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	project, err := first.CreateProject(t.Context(), "persisted")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := store.Open(t.Context(), dir, key, store.Options{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = second.Close() }()

	got, err := second.ProjectByName(t.Context(), "persisted")
	if err != nil {
		t.Fatalf("project after reopen: %v", err)
	}
	if got.ID != project.ID {
		t.Fatalf("project id %s after reopen, want %s", got.ID, project.ID)
	}
	p, err := second.ReadPragmas(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if p.UserVersion != store.SchemaVersion {
		t.Fatalf("user_version = %d after reopen, want %d", p.UserVersion, store.SchemaVersion)
	}
}

func TestSchemaRoundTrip(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)

	if err := s.SetAllowlist(ctx, project.ID, []string{"*.demo.test", "*.example.test"}); err != nil {
		t.Fatalf("set allowlist: %v", err)
	}
	patterns, err := s.Allowlist(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(patterns) != 2 {
		t.Fatalf("allowlist has %d patterns, want 2", len(patterns))
	}
	// Replacing the list must not accumulate stale patterns.
	if err := s.SetAllowlist(ctx, project.ID, []string{"*.demo.test"}); err != nil {
		t.Fatal(err)
	}
	if patterns, err = s.Allowlist(ctx, project.ID); err != nil || len(patterns) != 1 {
		t.Fatalf("allowlist after replace = %v (err %v), want one pattern", patterns, err)
	}

	admin := newPersona(t, s, project.ID, "admin")
	pro := newPersona(t, s, project.ID, "pro-user")

	got, err := s.Persona(ctx, admin.ID)
	if err != nil {
		t.Fatalf("persona: %v", err)
	}
	if got.Email != admin.Email || got.SeedState != store.SeedPending || got.TraitsJSON != "{}" {
		t.Fatalf("persona round trip mismatch: %+v", got)
	}
	if byEmail, err := s.PersonaByEmail(ctx, admin.Email); err != nil || byEmail.ID != admin.ID {
		t.Fatalf("PersonaByEmail = %v, %v", byEmail.ID, err)
	}
	if byName, err := s.PersonaByName(ctx, project.ID, "admin"); err != nil || byName.ID != admin.ID {
		t.Fatalf("PersonaByName = %v, %v", byName.ID, err)
	}

	if err := s.UpdateSeedState(ctx, pro.ID, store.SeedSeeded, "blobref"); err != nil {
		t.Fatalf("update seed state: %v", err)
	}
	if got, err = s.Persona(ctx, pro.ID); err != nil || got.SeedState != store.SeedSeeded {
		t.Fatalf("seed state = %q, %v", got.SeedState, err)
	}
	if got.UpdatedAt.Before(got.CreatedAt) {
		t.Error("updated_at moved backwards")
	}

	if err := s.SetRelation(ctx, store.Relation{
		ProjectID: project.ID, FromPersona: admin.ID, Kind: "approver_for", ToPersona: pro.ID,
	}); err != nil {
		t.Fatalf("set relation: %v", err)
	}
	// Reconciling the same YAML twice must not fail.
	if err := s.SetRelation(ctx, store.Relation{
		ProjectID: project.ID, FromPersona: admin.ID, Kind: "approver_for", ToPersona: pro.ID,
	}); err != nil {
		t.Fatalf("relation replay: %v", err)
	}
	rels, err := s.Relations(ctx, project.ID)
	if err != nil || len(rels) != 1 {
		t.Fatalf("relations = %v (err %v), want one", rels, err)
	}

	if err := s.PutSecret(ctx, admin.ID, store.SecretTOTPSeed, []byte("sealed-seed")); err != nil {
		t.Fatalf("put secret: %v", err)
	}
	if err := s.PutSecret(ctx, admin.ID, store.SecretTOTPSeed, []byte("resealed")); err != nil {
		t.Fatalf("replace secret: %v", err)
	}
	sec, err := s.Secret(ctx, admin.ID, store.SecretTOTPSeed)
	if err != nil || string(sec.ValueEnc) != "resealed" {
		t.Fatalf("secret = %q, %v", sec.ValueEnc, err)
	}
	if _, err := s.Secret(ctx, admin.ID, "custom:nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing secret err = %v, want ErrNotFound", err)
	}

	expiry := s.Now().Add(time.Hour)
	if err := s.PutSession(ctx, store.Session{
		PersonaID: admin.ID, Name: "default", BlobRef: store.NewID(), HintExpiresAt: expiry,
	}); err != nil {
		t.Fatalf("put session: %v", err)
	}
	sess, err := s.Session(ctx, admin.ID, "default")
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if !sess.HintExpiresAt.Equal(expiry) {
		t.Fatalf("hint_expires_at = %v, want %v", sess.HintExpiresAt, expiry)
	}
	if sess.SavedAt.IsZero() {
		t.Error("saved_at not stamped")
	}

	id, err := s.AppendLedger(ctx, store.LedgerEntry{
		ProjectID: project.ID, Actor: store.ActorREST, RunID: "run-1",
		PersonaID: admin.ID, Action: "secret.read",
	})
	if err != nil || id == 0 {
		t.Fatalf("append ledger = %d, %v", id, err)
	}
	entries, err := s.ListLedger(ctx, store.LedgerFilter{ProjectID: project.ID, RunID: "run-1"})
	if err != nil || len(entries) != 1 {
		t.Fatalf("ledger = %v (err %v), want one entry", entries, err)
	}
	if entries[0].PersonaID != admin.ID || entries[0].DetailJSON != "{}" {
		t.Fatalf("ledger entry mismatch: %+v", entries[0])
	}
}

func TestPersonaCascadeDeletesChildren(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)
	persona := newPersona(t, s, project.ID, "temp")

	if err := s.PutSecret(ctx, persona.ID, store.SecretTOTPSeed, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePersonaForTest(ctx, persona.ID); err != nil {
		t.Fatalf("delete persona: %v", err)
	}
	if _, err := s.Secret(ctx, persona.ID, store.SecretTOTPSeed); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("secret survived persona delete: %v", err)
	}
}

func TestMessageMatchingUsesEnvelopeRecipients(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)

	// A Bcc delivery: the header To is someone else entirely, and only
	// the envelope carries the real recipient.
	bcc, err := s.InsertMessage(ctx, store.Message{
		ProjectID: project.ID,
		FromAddr:  "noreply@app.test",
		Subject:   "Your code",
		TextBody:  "483920",
		Recipients: []store.Recipient{
			{Addr: "list@app.test", Kind: store.RecipientTo},
			{Addr: "hidden@demo.test", Kind: store.RecipientEnvelope},
		},
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	found, err := s.ListMessages(ctx, store.MessageFilter{
		ProjectID: project.ID, To: "hidden@demo.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != bcc.ID {
		t.Fatalf("envelope match returned %d messages, want the Bcc one", len(found))
	}
	if len(found[0].Recipients) != 2 {
		t.Fatalf("recipients not attached: %+v", found[0].Recipients)
	}

	// The header To address must not match, or Bcc recipients would leak
	// into another persona's inbox.
	none, err := s.ListMessages(ctx, store.MessageFilter{
		ProjectID: project.ID, To: "list@app.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("header To matched %d messages, want 0", len(none))
	}
}

func TestListMessagesQuarantineAndSince(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)
	addr := "user@demo.test"

	base := s.Now()
	insert := func(subject string, at time.Time, quarantined bool) store.Message {
		t.Helper()
		m, err := s.InsertMessage(ctx, store.Message{
			ProjectID: project.ID, FromAddr: "app@test", Subject: subject,
			Quarantined: quarantined, ReceivedAt: at,
			Recipients: []store.Recipient{{Addr: addr, Kind: store.RecipientEnvelope}},
		})
		if err != nil {
			t.Fatalf("insert %s: %v", subject, err)
		}
		return m
	}
	old := insert("old", base, false)
	recent := insert("recent", base.Add(time.Second), false)
	insert("quarantined", base.Add(2*time.Second), true)

	visible, err := s.ListMessages(ctx, store.MessageFilter{ProjectID: project.ID, To: addr})
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 2 {
		t.Fatalf("got %d visible messages, want 2 (quarantined excluded)", len(visible))
	}
	if visible[0].ID != recent.ID {
		t.Fatalf("newest first violated: got %q", visible[0].Subject)
	}

	since, err := s.ListMessages(ctx, store.MessageFilter{
		ProjectID: project.ID, To: addr, Since: old.ReceivedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != 1 || since[0].ID != recent.ID {
		t.Fatalf("since is not exclusive: got %d messages", len(since))
	}

	all, err := s.ListMessages(ctx, store.MessageFilter{
		ProjectID: project.ID, To: addr, IncludeQuarantined: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("dashboard view returned %d messages, want 3", len(all))
	}

	limited, err := s.ListMessages(ctx, store.MessageFilter{ProjectID: project.ID, To: addr, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 {
		t.Fatalf("limit ignored: got %d", len(limited))
	}
}

func TestDeleteMessageReturnsBlobRefs(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)

	rawRef, err := s.Blobs.Put([]byte("raw mime"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.InsertMessage(ctx, store.Message{
		ProjectID: project.ID, FromAddr: "a@b.test", Subject: "s", RawRef: rawRef,
		Recipients: []store.Recipient{{Addr: "u@demo.test", Kind: store.RecipientEnvelope}},
	})
	if err != nil {
		t.Fatal(err)
	}
	gotRaw, gotHTML, err := s.DeleteMessage(ctx, m.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if gotRaw != rawRef || gotHTML != "" {
		t.Fatalf("refs = %q/%q, want %q/\"\"", gotRaw, gotHTML, rawRef)
	}
	if _, err := s.Message(ctx, m.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("message survived delete: %v", err)
	}
}

// TestSensitiveColumnsSealedOnDisk proves the database and blob files
// carry no plaintext OTP or magic link, which is what makes a backup that
// excludes the key file safe to keep. It is deliberately not a claim about
// the whole data directory: the key file lives there, so anyone holding
// the directory holds the key.
func TestSensitiveColumnsSealedOnDisk(t *testing.T) {
	const otp = "483920"
	const link = "https://app.test/verify?token=supersecrettoken"

	dir := t.TempDir()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "keys"), "test")
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(t.Context(), dir, key, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(t.Context(), "sealed")
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.InsertMessage(t.Context(), store.Message{
		ProjectID: project.ID, FromAddr: "noreply@app.test", Subject: "Verify your email",
		TextBody:      "Your code is " + otp + " and your link is " + link,
		ExtractedJSON: fmt.Sprintf(`{"otp_best":%q,"verify_link_best":%q}`, otp, link),
		Recipients:    []store.Recipient{{Addr: "u@demo.test", Kind: store.RecipientEnvelope}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Round trip through the store still yields plaintext to callers.
	got, err := s.Message(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.TextBody, otp) || !strings.Contains(got.ExtractedJSON, otp) {
		t.Fatalf("round trip lost content: body=%q extraction=%q", got.TextBody, got.ExtractedJSON)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		// nolint:gosec // path comes from WalkDir over this test temp dir.
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, secret := range []string{otp, link} {
			if bytes.Contains(content, []byte(secret)) {
				t.Errorf("%s leaks %q in plaintext", filepath.Base(path), secret)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSetExtraction(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)

	m, err := s.InsertMessage(ctx, store.Message{
		ProjectID: project.ID, FromAddr: "a@b.test", Subject: "s", TextBody: "code 111222",
		Recipients: []store.Recipient{{Addr: "u@demo.test", Kind: store.RecipientEnvelope}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Before extraction runs the result is absent, which the API reports
	// as extracted: null.
	if got, err := s.Message(ctx, m.ID); err != nil || got.ExtractedJSON != "" {
		t.Fatalf("extraction = %q, %v; want empty", got.ExtractedJSON, err)
	}
	if err := s.SetExtraction(ctx, m.ID, `{"otp_best":"111222"}`); err != nil {
		t.Fatalf("set extraction: %v", err)
	}
	got, err := s.Message(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExtractedJSON != `{"otp_best":"111222"}` {
		t.Fatalf("extraction = %q", got.ExtractedJSON)
	}
	if err := s.SetExtraction(ctx, "missingid1234", "{}"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestOpenRequiresSealer(t *testing.T) {
	if _, err := store.Open(t.Context(), t.TempDir(), nil, store.Options{}); err == nil {
		t.Fatal("opened a credential store with no encryption")
	}
}

func TestNotFoundEverywhere(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	for name, err := range map[string]error{
		"project":  errOf(func() error { _, e := s.Project(ctx, "missingid1234"); return e }),
		"persona":  errOf(func() error { _, e := s.Persona(ctx, "missingid1234"); return e }),
		"message":  errOf(func() error { _, e := s.Message(ctx, "missingid1234"); return e }),
		"session":  errOf(func() error { _, e := s.Session(ctx, "missingid1234", "default"); return e }),
		"seed":     s.UpdateSeedState(ctx, "missingid1234", store.SeedSeeded, ""),
		"byname":   errOf(func() error { _, e := s.PersonaByName(ctx, "p", "n"); return e }),
		"projname": errOf(func() error { _, e := s.ProjectByName(ctx, "nope"); return e }),
	} {
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
}

func errOf(fn func() error) error { return fn() }

// TestConcurrentWritersAndReaders is the WAL and busy_timeout smoke test:
// many goroutines writing through the single write executor while readers
// query the pool, all under -race.
func TestConcurrentWritersAndReaders(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)
	addr := "load@demo.test"

	const writers, perWriter = 8, 25
	var wg sync.WaitGroup
	errCh := make(chan error, writers*2)

	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perWriter {
				_, err := s.InsertMessage(ctx, store.Message{
					ProjectID: project.ID,
					FromAddr:  "app@test",
					Subject:   fmt.Sprintf("w%d-%d", w, i),
					Recipients: []store.Recipient{
						{Addr: addr, Kind: store.RecipientEnvelope},
					},
				})
				if err != nil {
					errCh <- fmt.Errorf("writer %d: %w", w, err)
					return
				}
			}
		}()
	}
	for r := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perWriter {
				if _, err := s.ListMessages(ctx, store.MessageFilter{
					ProjectID: project.ID, To: addr, Limit: 10,
				}); err != nil {
					errCh <- fmt.Errorf("reader %d: %w", r, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	all, err := s.ListMessages(ctx, store.MessageFilter{ProjectID: project.ID, To: addr})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != writers*perWriter {
		t.Fatalf("stored %d messages, want %d", len(all), writers*perWriter)
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	dir := t.TempDir()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "keys"), "test")
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(t.Context(), dir, key, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BumpSchemaVersionForTest(t.Context(), 99); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(t.Context(), dir, key, store.Options{}); err == nil {
		t.Fatal("opened a database written by a newer binary")
	}
}

func TestDataDirPermissions(t *testing.T) {
	skipOnWindows(t, "POSIX mode bits; Windows reports 777 for a directory created 0700")
	s := openTestStore(t)
	for _, dir := range []string{s.DataDir(), filepath.Join(s.DataDir(), "blobs")} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("%s mode %o, want 700", dir, perm)
		}
	}
}

func TestStoreClockIsInjectable(t *testing.T) {
	dir := t.TempDir()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "keys"), "test")
	if err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	s, err := store.Open(t.Context(), dir, key, store.Options{Now: func() time.Time { return fixed }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	p, err := s.CreateProject(t.Context(), "clock")
	if err != nil {
		t.Fatal(err)
	}
	if !p.CreatedAt.Equal(fixed) {
		t.Fatalf("created_at = %v, want the injected clock %v", p.CreatedAt, fixed)
	}
}
