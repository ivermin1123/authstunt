package main_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ivermin1123/authstunt/internal/store"
)

// TestPooledPolicyReachesTheServiceFromTheFlag covers the same class of
// defect as the bus wiring next door, on the one dependency that had no
// cover at all.
//
// --pooled-max-delivery-latency is the only path from the command line to
// personas.Config.Pooled, and every test that exercises pooled mode sets the
// policy on the config by hand. Deleting the three lines in serve that read
// the flag would leave all eleven of them green while the shipped binary
// refused every pooled request, so the flag is driven here through the
// process, its startup line, its health endpoint and a real acquire.
//
// The acquire is the load-bearing assertion. Without a policy the service
// refuses a pooled request as pooled_policy_missing before it even lists the
// pool; with one it gets past that gate. The two answers differ by exactly
// the flag, which is the wiring this test exists to prove.
func TestPooledPolicyReachesTheServiceFromTheFlag(t *testing.T) {
	bootstrap := []string{"--project", "demo", "--domain", "demo.test"}

	t.Run("with the flag", func(t *testing.T) {
		dataDir := t.TempDir()
		bearer := provisionBearer(t, dataDir, bootstrap...)
		srv := startBinary(t, dataDir,
			append(bootstrap, "--pooled-max-delivery-latency", "2s")...)
		if srv.apiAddr == "" {
			t.Fatal("the ready line carried no API address")
		}

		// The startup line is the cheapest place a degraded instance can
		// declare itself, and an operator who passed the flag should be
		// able to see that it took.
		if ready := srv.stdout(); !strings.Contains(ready, "pooled on") {
			t.Errorf("the ready line does not report pooled as on: %q", ready)
		}

		// The machine-readable half of the same answer. Nothing had ever
		// asserted this field was true against a running binary.
		health := apiCall(t, srv.apiAddr, http.MethodGet, "/healthz", bearer, nil)
		if configured, _ := health["pooled_configured"].(bool); !configured {
			t.Errorf("healthz reported pooled_configured=false with the flag set: %v", health)
		}

		// An empty pool is nothing found, not a seed that failed. The
		// distinction matters to whoever reads the refusal: one says the
		// account could not be prepared, the other says there was no
		// account to prepare.
		if got := pooledRefusal(t, srv.apiAddr, bearer); got != "not_found" {
			t.Errorf("a pooled request over an empty pool was refused as %q, "+
				"want not_found", got)
		}

		// Enabled and empty is a configuration that cannot serve anything,
		// so it says so at startup rather than through a refused lease.
		if logs := srv.logs(); !strings.Contains(logs, "the pool is empty") {
			t.Errorf("pooled mode was enabled over an empty pool without saying "+
				"so at startup:\n%s", logs)
		}
	})

	t.Run("without the flag", func(t *testing.T) {
		dataDir := t.TempDir()
		bearer := provisionBearer(t, dataDir, bootstrap...)
		srv := startBinary(t, dataDir, bootstrap...)

		if ready := srv.stdout(); !strings.Contains(ready, "pooled off") {
			t.Errorf("the ready line does not report pooled as off: %q", ready)
		}
		health := apiCall(t, srv.apiAddr, http.MethodGet, "/healthz", bearer, nil)
		if configured, _ := health["pooled_configured"].(bool); configured {
			t.Errorf("healthz reported pooled_configured=true with no flag: %v", health)
		}
		// The empty pool is only worth a warning when somebody asked for
		// pooled mode. An instance that never wanted it says nothing.
		if logs := srv.logs(); strings.Contains(logs, "the pool is empty") {
			t.Errorf("an instance that never enabled pooled mode warned about "+
				"the pool:\n%s", logs)
		}
		// The refusal is the named one, never a quiet downgrade to
		// ephemeral.
		if got := pooledRefusal(t, srv.apiAddr, bearer); got != store.ReasonPooledPolicyMissing {
			t.Errorf("pooled refusal = %q, want %q", got, store.ReasonPooledPolicyMissing)
		}
	})
}

// pooledRefusal asks the running binary for a pooled lease and returns the
// reason code it came back with.
//
// A pooled acquire cannot succeed against a shipped binary today: the pool
// is only ever read, never written, so it is always empty. The reason code
// is still the honest signal for what this test is about, because the policy
// gate is checked before the pool is listed.
//
// The two codes are deliberately different answers. Missing policy is a
// refusal to serve pooled mode at all; an empty pool is nothing found, which
// is why it reports not_found rather than a failure to prepare an account.
func pooledRefusal(t *testing.T, apiAddr, bearer string) string {
	t.Helper()
	run := apiCall(t, apiAddr, http.MethodPost, "/api/v1/runs", bearer, nil)
	runID, runToken := str(t, run, "run_id"), str(t, run, "run_token")

	_, err := apiRequest(apiAddr, http.MethodPost, "/api/v1/runs/"+runID+"/leases",
		runToken, map[string]string{"role": "signup", "mode": store.ModePooled})
	if err == nil {
		t.Fatal("a pooled lease succeeded, which means the pool can now be " +
			"populated: this helper and its callers need revisiting")
	}
	switch {
	case strings.Contains(err.Error(), store.ReasonPooledPolicyMissing):
		return store.ReasonPooledPolicyMissing
	case strings.Contains(err.Error(), "not_found"):
		return "not_found"
	default:
		t.Fatalf("unexpected pooled refusal: %v", err)
		return ""
	}
}

// TestTheReadyLineReportsTheCapabilitiesItWasGiven pins the other fields of
// the startup line, which were added and then asserted nowhere.
//
// The line exists so a degraded instance says so at startup rather than at
// the first test that depends on the missing piece. A line nothing asserts
// is a line that can quietly stop being true.
func TestTheReadyLineReportsTheCapabilitiesItWasGiven(t *testing.T) {
	dataDir := t.TempDir()
	srv := startBinary(t, dataDir, "--project", "demo", "--domain", "demo.test")

	// A plain serve: the bus is always wired, and neither optional
	// dependency was asked for.
	ready := srv.stdout()
	for _, want := range []string{"long-poll on", "pooled off", "seeder off"} {
		if !strings.Contains(ready, want) {
			t.Errorf("the ready line does not report %q: %q", want, ready)
		}
	}
}
