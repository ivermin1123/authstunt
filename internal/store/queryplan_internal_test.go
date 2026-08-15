package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivermin1123/authstunt/internal/secrets"
)

// The tests here are in the store package rather than store_test because
// the thing under test is not a result but a plan: they ask SQLite how it
// intends to answer a query, which needs the read handle and the query
// text the production path actually uses.
//
// An index that exists is not an index that is used. Both invariants below
// were false or nearly false once, in ways no result-level test could see,
// because a full scan and an index seek return exactly the same rows.

// queryPlan returns EXPLAIN QUERY PLAN output as one line per step.
func queryPlan(t *testing.T, s *Store, query string, args ...any) []string {
	t.Helper()
	rows, err := s.read.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		out = append(out, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("the planner returned no steps")
	}
	return out
}

func planStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "keys"), "test")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	s, err := Open(context.Background(), dir, key, Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestPendingExtractionsQueryUsesItsIndex pins the recovery read to its
// partial index.
//
// This ran as a full table scan for as long as the index existed. The
// index is partial, so SQLite can only use it when the query's state term
// is a literal it can see while preparing; the query bound a parameter
// instead, and the planner answered by reading every message row at every
// boot. Nothing about the results changed, which is why it went unnoticed.
func TestPendingExtractionsQueryUsesItsIndex(t *testing.T) {
	plan := queryPlan(t, planStore(t), pendingExtractionsQuery)
	joined := strings.Join(plan, "\n")

	if !strings.Contains(joined, "messages_extraction_pending") {
		t.Errorf("the recovery read does not use messages_extraction_pending:\n%s", joined)
	}
	// A step that scans messages without naming an index is the full
	// table read this test exists to catch.
	for _, step := range plan {
		if strings.HasPrefix(step, "SCAN m") && !strings.Contains(step, "INDEX") {
			t.Errorf("the recovery read scans every message row: %q\nfull plan:\n%s", step, joined)
		}
	}
}

// TestClaimCandidatesQueryUsesItsIndex pins the read a parked claim
// repeats every time it is woken. Without message_bindings_lease it is a
// scan of every binding in the project, paid per wakeup rather than per
// boot.
func TestClaimCandidatesQueryUsesItsIndex(t *testing.T) {
	const query = `SELECT m.id, m.received_at, m.extracted_json, m.extraction_state,
		       m.quarantined, b.suspect,
		       EXISTS (SELECT 1 FROM claims c
		               WHERE c.message_id = m.id AND c.kind = ?) AS claimed
		  FROM message_bindings b
		  JOIN messages m ON m.id = b.message_id
		 WHERE b.lease_id = ?
		 ORDER BY m.received_at, m.id`

	plan := queryPlan(t, planStore(t), query, ClaimEmailOTP, "lease-id")
	joined := strings.Join(plan, "\n")

	if !strings.Contains(joined, "message_bindings_lease") {
		t.Errorf("the candidate read does not use message_bindings_lease:\n%s", joined)
	}
	for _, step := range plan {
		if strings.HasPrefix(step, "SCAN b") && !strings.Contains(step, "INDEX") {
			t.Errorf("the candidate read scans every binding: %q\nfull plan:\n%s", step, joined)
		}
	}
}
