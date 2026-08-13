package main_test

import (
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivermin1123/authstunt/internal/ledger"
	"github.com/ivermin1123/authstunt/internal/secrets"
	"github.com/ivermin1123/authstunt/internal/store"
)

// bearerCmd runs one bearer operation and returns its streams separately.
//
// stdout and stderr are kept apart on purpose: which stream carried the
// credential is the property most of these tests are about, and
// CombinedOutput would erase exactly that.
func bearerCmd(t *testing.T, dataDir, operation string, args ...string) (string, string, error) {
	t.Helper()
	full := append([]string{"project", "bearer", operation, "--data-dir", dataDir}, args...)
	// nolint:gosec // G204 flags the variable program and arguments. Both
	// come from this test: the program is the binary it just built, and
	// the arguments are its own literals.
	cmd := exec.Command(binary(t), full...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// initFlags initializes an empty data directory in the same call.
func initFlags() []string {
	return []string{"--project", "demo", "--domain", "demo.test"}
}

// openForTest opens a data directory the way a second tool would.
func openForTest(t *testing.T, dataDir string) *store.Store {
	t.Helper()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dataDir, "keys"), "project")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	st, err := store.Open(t.Context(), dataDir, key, store.Options{Logger: logger})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return st
}

// authenticates reports whether a token still resolves to a project.
func authenticates(t *testing.T, dataDir, token string) bool {
	t.Helper()
	st := openForTest(t, dataDir)
	_, err := st.ProjectByBearer(t.Context(), token)
	return err == nil
}

// TestBearerRevealIsRefusedWithoutATTY is the finding this whole change
// answers, moved to the one place a credential is allowed to exist.
//
// A test's stdout is a pipe, which is the same shape as a CI log, a file
// and a `| tee`. The default has to be refusal, because this program
// cannot see the far end of any of them.
func TestBearerRevealIsRefusedWithoutATTY(t *testing.T) {
	dataDir := t.TempDir()
	stdout, stderr, err := bearerCmd(t, dataDir, "provision", initFlags()...)
	if err == nil {
		t.Fatal("the credential was printed to a pipe with no opt-in")
	}
	for stream, body := range map[string]string{"stdout": stdout, "stderr": stderr} {
		if strings.Contains(body, store.ProjectBearerPrefix) {
			t.Errorf("the refusal wrote something bearer-shaped to %s: %q", stream, body)
		}
	}
	if !strings.Contains(stderr, "--allow-non-tty-reveal") {
		t.Errorf("the refusal did not name the opt-in: %q", stderr)
	}

	// Nothing was minted. The check runs before the write, so a refused
	// reveal never leaves a credential nobody can read.
	st := openForTest(t, dataDir)
	project, err := st.SingleProject(t.Context())
	if err != nil {
		t.Fatalf("single project: %v", err)
	}
	if has, err := st.ProjectHasBearer(t.Context(), project.ID); err != nil {
		t.Fatalf("bearer state: %v", err)
	} else if has {
		t.Error("a refused reveal still minted a credential, which nobody can now read")
	}
}

// TestBearerRevealOnTheOptInPrintsOnceOnStdout covers the deliberate
// override: the value appears exactly once, on stdout alone, and the
// operator is told they now own the destination.
func TestBearerRevealOnTheOptInPrintsOnceOnStdout(t *testing.T) {
	dataDir := t.TempDir()
	stdout, stderr, err := bearerCmd(t, dataDir, "provision",
		append(initFlags(), "--allow-non-tty-reveal")...)
	if err != nil {
		t.Fatalf("provision: %v\n%s", err, stderr)
	}
	if got := strings.Count(stdout, store.ProjectBearerPrefix); got != 1 {
		t.Fatalf("expected exactly one credential on stdout, got %d: %q", got, stdout)
	}
	token := strings.TrimSpace(stdout)
	if strings.Contains(stderr, token) || strings.Contains(stderr, store.ProjectBearerPrefix) {
		t.Errorf("the credential also reached stderr: %q", stderr)
	}
	if !strings.Contains(stderr, "warning") {
		t.Errorf("the non-terminal reveal carried no warning: %q", stderr)
	}
	if !authenticates(t, dataDir, token) {
		t.Error("the printed credential does not authenticate")
	}
	assertNotOnDisk(t, dataDir, token)
}

// TestBearerProvisionRefusesToOverwrite keeps a re-run from cutting off
// every consumer of the current value. Replacing a live credential is
// rotation, and rotation has its own word.
func TestBearerProvisionRefusesToOverwrite(t *testing.T) {
	dataDir := t.TempDir()
	token := provisionBearer(t, dataDir, initFlags()...)

	_, stderr, err := bearerCmd(t, dataDir, "provision", "--allow-non-tty-reveal")
	if err == nil {
		t.Fatal("provision overwrote a live credential")
	}
	if !strings.Contains(stderr, "rotate") {
		t.Errorf("the refusal did not name rotate: %q", stderr)
	}
	if !authenticates(t, dataDir, token) {
		t.Error("the refused provision invalidated the current credential anyway")
	}
}

// TestBearerRotateInvalidatesThePreviousValue is the rotation contract:
// one live credential per project, and the previous one stops working the
// moment the new one exists.
func TestBearerRotateInvalidatesThePreviousValue(t *testing.T) {
	dataDir := t.TempDir()
	first := provisionBearer(t, dataDir, initFlags()...)

	stdout, stderr, err := bearerCmd(t, dataDir, "rotate", "--allow-non-tty-reveal")
	if err != nil {
		t.Fatalf("rotate: %v\n%s", err, stderr)
	}
	second := strings.TrimSpace(stdout)
	if second == first {
		t.Fatal("rotation returned the same value")
	}
	if authenticates(t, dataDir, first) {
		t.Error("the previous credential still authenticates after a rotation")
	}
	if !authenticates(t, dataDir, second) {
		t.Error("the rotated credential does not authenticate")
	}
	if !strings.Contains(stderr, "no longer authenticates") {
		t.Errorf("rotation did not say the old value was cut off: %q", stderr)
	}
}

// TestARefusedRotationLeavesTheCurrentValueWorking pins the ordering that
// makes the refusal safe.
//
// Minting first and refusing to print afterwards would be the worst
// outcome available: the old credential dead, the new one unknowable, and
// a lockout caused by the safety check itself.
func TestARefusedRotationLeavesTheCurrentValueWorking(t *testing.T) {
	dataDir := t.TempDir()
	token := provisionBearer(t, dataDir, initFlags()...)

	if _, _, err := bearerCmd(t, dataDir, "rotate"); err == nil {
		t.Fatal("rotate printed a credential to a pipe with no opt-in")
	}
	if !authenticates(t, dataDir, token) {
		t.Error("a refused rotation destroyed the working credential")
	}
}

// TestBearerRotateNeedsAnExistingBearer tells the two operations apart.
// An operator who typed rotate believes a value exists somewhere; being
// told there never was one is the useful answer.
func TestBearerRotateNeedsAnExistingBearer(t *testing.T) {
	dataDir := t.TempDir()
	_, stderr, err := bearerCmd(t, dataDir, "rotate",
		append(initFlags(), "--allow-non-tty-reveal")...)
	if err == nil {
		t.Fatal("rotate invented a first credential")
	}
	if !strings.Contains(stderr, "provision") {
		t.Errorf("the refusal did not name provision: %q", stderr)
	}
}

// TestBearerRevokeStopsAuthenticationAndTheAPI covers the third operation
// end to end: the credential stops working, and the surface it guarded
// refuses to start rather than binding an API nobody can reach.
func TestBearerRevokeStopsAuthenticationAndTheAPI(t *testing.T) {
	dataDir := t.TempDir()
	token := provisionBearer(t, dataDir, initFlags()...)

	if _, stderr, err := bearerCmd(t, dataDir, "revoke"); err != nil {
		t.Fatalf("revoke: %v\n%s", err, stderr)
	}
	if authenticates(t, dataDir, token) {
		t.Error("the revoked credential still authenticates")
	}
	// Revoking twice is not an error: the caller asked for a state and
	// the state already holds.
	if _, stderr, err := bearerCmd(t, dataDir, "revoke"); err != nil {
		t.Errorf("a second revoke failed: %v\n%s", err, stderr)
	}

	out, err := runServeExpectingFailure(t, dataDir)
	if err == nil {
		t.Fatal("serve started the API after the bearer was revoked")
	}
	if !strings.Contains(out, "project bearer provision") {
		t.Errorf("the refusal did not name the provisioning command: %q", out)
	}
}

// TestBearerChangesAreAuditedWithoutTheValue checks the trail records that
// a credential changed and records nothing that helps anybody work out
// which value it was.
func TestBearerChangesAreAuditedWithoutTheValue(t *testing.T) {
	dataDir := t.TempDir()
	first := provisionBearer(t, dataDir, initFlags()...)
	stdout, stderr, err := bearerCmd(t, dataDir, "rotate", "--allow-non-tty-reveal")
	if err != nil {
		t.Fatalf("rotate: %v\n%s", err, stderr)
	}
	second := strings.TrimSpace(stdout)
	if _, stderr, err := bearerCmd(t, dataDir, "revoke"); err != nil {
		t.Fatalf("revoke: %v\n%s", err, stderr)
	}

	st := openForTest(t, dataDir)
	entries, err := st.ListLedger(t.Context(), store.LedgerFilter{})
	if err != nil {
		t.Fatalf("list ledger: %v", err)
	}
	var operations []string
	for _, e := range entries {
		if strings.Contains(e.DetailJSON, first) || strings.Contains(e.DetailJSON, second) {
			t.Fatalf("the ledger recorded a credential: %s", e.DetailJSON)
		}
		if strings.Contains(e.DetailJSON, store.ProjectBearerPrefix) {
			t.Fatalf("the ledger recorded something bearer-shaped: %s", e.DetailJSON)
		}
		if e.Action == ledger.ActionBearerChanged {
			operations = append(operations, e.DetailJSON)
		}
	}
	want := []string{
		`{"operation":"` + ledger.BearerProvisioned + `"}`,
		`{"operation":"` + ledger.BearerRotated + `"}`,
		`{"operation":"` + ledger.BearerRevoked + `"}`,
	}
	if len(operations) != len(want) {
		t.Fatalf("expected %d bearer events, got %d: %v", len(want), len(operations), operations)
	}
	for i, w := range want {
		if operations[i] != w {
			t.Errorf("bearer event %d = %s, want %s", i, operations[i], w)
		}
	}
}
