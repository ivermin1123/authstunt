package smtp_test

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ivermin1123/authstunt/internal/secrets"
	"github.com/ivermin1123/authstunt/internal/smtp"
	"github.com/ivermin1123/authstunt/internal/sse"
	"github.com/ivermin1123/authstunt/internal/store"
)

// benchTemplate is one ordinary code mail, sized like the real thing so
// the blob writes on the measured path are the ones production performs.
const benchTemplate = "From: app@acme.example\r\n" +
	"To: %s\r\n" +
	"Date: %s\r\n" +
	"Subject: Your verification code\r\n" +
	"\r\n" +
	"Your code is %s. It expires in ten minutes.\r\n"

// BenchmarkIngestAck measures what an SMTP client waits for.
//
// Deliver returns when the message is durably stored, and the session
// answers 250 immediately after, so the time of one call is the ack
// latency of one message. Deliveries are sequential because that is the
// shape the number is wanted in: one client, one message, how long until
// it is told the mail is safe. A concurrent version would measure
// throughput, which is a different question and is bounded by the single
// writer rather than by the storage barrier.
//
// The distribution is reported rather than only the mean, because the
// cost this exists to expose is a per-commit disk barrier, and a barrier
// shows up in the tail before it shows up in the average.
func BenchmarkIngestAck(b *testing.B) {
	const domain = "demo.test"
	dir := b.TempDir()

	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "keys"), "bench")
	if err != nil {
		b.Fatalf("key: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	st, err := store.Open(b.Context(), dir, key, store.Options{Logger: logger})
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	b.Cleanup(func() {
		if err := st.Close(); err != nil {
			b.Errorf("close store: %v", err)
		}
	})

	project, err := st.CreateProject(b.Context(), "bench")
	if err != nil {
		b.Fatalf("project: %v", err)
	}
	allowlist := []string{domain}
	if err := st.SetAllowlist(b.Context(), project.ID, allowlist); err != nil {
		b.Fatalf("allowlist: %v", err)
	}

	generation, err := st.NextEventGeneration(b.Context())
	if err != nil {
		b.Fatalf("generation: %v", err)
	}
	bus := sse.NewBus(generation)
	busCtx, stopBus := context.WithCancel(context.Background())
	go bus.Run(busCtx)
	b.Cleanup(stopBus)

	// The bus is drained for the whole run. Nothing here asserts on the
	// events, but a bus nobody reads fills its buffer and turns Publish
	// into a wait that would be measured as ack latency it is not.
	sub, err := bus.SubscribeFrom(busCtx, sse.EventID{})
	if err != nil {
		b.Fatalf("subscribe: %v", err)
	}
	go func() {
		for range sub.Events() { //nolint:revive // draining is the point
		}
	}()

	ingest, err := smtp.NewIngest(smtp.IngestConfig{
		Store: st, Bus: bus, ProjectID: project.ID, Allowlist: allowlist, Logger: logger,
	})
	if err != nil {
		b.Fatalf("ingest: %v", err)
	}
	// Extraction runs as it does in production: after the ack, on its own
	// workers. It is started so the measured path carries the same
	// concurrent load it carries when serving.
	ingest.Start(busCtx)
	b.Cleanup(ingest.Stop)

	pragmas, err := st.ReadPragmas(b.Context())
	if err != nil {
		b.Fatalf("pragmas: %v", err)
	}
	b.Logf("synchronous=%d journal_mode=%s", pragmas.Synchronous, pragmas.JournalMode)

	took := make([]time.Duration, 0, b.N)

	b.ResetTimer()
	for i := range b.N {
		to := fmt.Sprintf("bench-%d@%s", i, domain)
		raw := []byte(fmt.Sprintf(benchTemplate, to, time.Now().Format(time.RFC1123Z), "482913"))
		start := time.Now()
		if err := ingest.Deliver(b.Context(), smtp.Delivery{
			From:       "app@acme.example",
			Recipients: []string{to},
			Raw:        raw,
			ReceivedAt: time.Now(),
		}); err != nil {
			b.Fatalf("deliver: %v", err)
		}
		took = append(took, time.Since(start))
	}
	b.StopTimer()

	if len(took) == 0 {
		return
	}
	slices.Sort(took)
	at := func(q float64) float64 {
		i := int(q * float64(len(took)-1))
		return float64(took[i]) / float64(time.Millisecond)
	}
	b.ReportMetric(at(0.50), "p50_ms")
	b.ReportMetric(at(0.95), "p95_ms")
	b.ReportMetric(at(0.99), "p99_ms")
	b.ReportMetric(float64(took[len(took)-1])/float64(time.Millisecond), "max_ms")
}
