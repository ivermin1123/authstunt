package store_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/ivermin1123/authstunt/internal/secrets"
	"github.com/ivermin1123/authstunt/internal/store"
)

// The driver was chosen for read-heavy work: dashboard queries, matcher
// scans, ledger reads. These benchmarks measure exactly those paths, so
// the choice stays falsifiable instead of resting on a third-party
// benchmark. They report numbers and never gate CI.

func benchStore(b *testing.B, messages int) (*store.Store, store.Project, string) {
	b.Helper()
	dir := b.TempDir()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "keys"), "bench")
	if err != nil {
		b.Fatal(err)
	}
	s, err := store.Open(b.Context(), dir, key, store.Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })

	project, err := s.CreateProject(b.Context(), "bench")
	if err != nil {
		b.Fatal(err)
	}
	addr := "bench@demo.test"
	for i := range messages {
		_, err := s.InsertMessage(b.Context(), store.Message{
			ProjectID: project.ID,
			FromAddr:  "noreply@app.test",
			Subject:   fmt.Sprintf("Verify your email (%d)", i),
			TextBody:  fmt.Sprintf("Your code is %06d. It expires in 10 minutes.", i),
			Recipients: []store.Recipient{
				{Addr: addr, Kind: store.RecipientEnvelope},
				{Addr: addr, Kind: store.RecipientTo},
			},
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	return s, project, addr
}

// BenchmarkListMessages is the dashboard inbox query.
func BenchmarkListMessages(b *testing.B) {
	s, project, addr := benchStore(b, 1000)
	filter := store.MessageFilter{ProjectID: project.ID, To: addr, Limit: 50}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.ListMessages(b.Context(), filter); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMatcherScan is the long-poll matcher: newest message for one
// address, the query every `?wait=` request runs before it parks.
func BenchmarkMatcherScan(b *testing.B) {
	s, project, addr := benchStore(b, 1000)
	filter := store.MessageFilter{ProjectID: project.ID, To: addr, Limit: 1}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.ListMessages(b.Context(), filter); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMessageByID is the single-message read behind /otp and /links.
func BenchmarkMessageByID(b *testing.B) {
	s, project, addr := benchStore(b, 100)
	found, err := s.ListMessages(b.Context(), store.MessageFilter{
		ProjectID: project.ID, To: addr, Limit: 1,
	})
	if err != nil || len(found) == 0 {
		b.Fatalf("seed lookup: %v", err)
	}
	id := found[0].ID
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.Message(b.Context(), id); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkInsertMessage is the ingest write path.
func BenchmarkInsertMessage(b *testing.B) {
	s, project, addr := benchStore(b, 0)
	b.ReportAllocs()
	for b.Loop() {
		_, err := s.InsertMessage(b.Context(), store.Message{
			ProjectID: project.ID, FromAddr: "noreply@app.test", Subject: "Verify",
			TextBody:   "Your code is 483920.",
			Recipients: []store.Recipient{{Addr: addr, Kind: store.RecipientEnvelope}},
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}
