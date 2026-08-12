package ledger_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ivermin1123/authstunt/internal/ledger"
)

// allEvents is every event the package defines, populated with values a
// careless call site might pass. The tests below run over this list, so an
// event added without being registered here fails the completeness check
// rather than quietly escaping every rule.
func allEvents() []ledger.Event {
	return []ledger.Event{
		ledger.MailReceived{
			MessageID:    "msg000000001",
			EnvelopeFrom: "bounce-handler@acme.example",
		},
		ledger.MailQuarantined{
			MessageID:  "msg000000002",
			Recipients: ledger.Addrs{"realcustomer@gmail.com", "someone.else@yahoo.com"},
		},
		ledger.ExtractionFailed{
			MessageID: "msg000000003",
			Reason:    "extraction panicked",
		},
		ledger.LeaseAcquired{
			LeaseID:    "lease0000001",
			IdentityID: "iden00000001",
			Role:       "pro",
			Mode:       "ephemeral",
			Addr:       "pro-a1b2c3d4e5f6@demo.test",
		},
		ledger.LeaseRefused{
			Role:   "pro",
			Mode:   "pooled",
			Reason: "pooled_policy_missing",
		},
		ledger.LeaseReleased{
			LeaseID:    "lease0000002",
			IdentityID: "iden00000002",
			Addr:       "pro-a1b2c3d4e5f6@demo.test",
		},
		ledger.SeedSettled{
			LeaseID:     "lease0000003",
			IdentityID:  "iden00000003",
			SeedState:   "failed",
			Addr:        "pro-a1b2c3d4e5f6@demo.test",
			Fingerprint: "v1-abc",
			Reason:      "seed endpoint answered 500 Internal Server Error",
		},
		ledger.MailBound{
			MessageID:  "msg000000004",
			LeaseID:    "lease0000004",
			IdentityID: "iden00000004",
			Addr:       "pro-a1b2c3d4e5f6@demo.test",
			Suspect:    "cooldown",
		},
		ledger.MailUnbound{
			MessageID:  "msg000000005",
			Recipients: ledger.Addrs{"realcustomer@gmail.com"},
		},
		ledger.ClaimSettled{
			LeaseID:   "lease0000005",
			ClaimID:   "clam00000001",
			Kind:      "email_otp",
			MessageID: "msg000000006",
			Reason:    "claim_ok",
			Addr:      "pro-a1b2c3d4e5f6@demo.test",
		},
	}
}

// TestEveryEventRedactsItsAddresses is the reason Addr is a type and not a
// string. A call site that forgot to redact would be a leak that reviewed
// clean, so the redaction happens where it cannot be skipped.
func TestEveryEventRedactsItsAddresses(t *testing.T) {
	leaks := []string{
		"bounce-handler@acme.example",
		"realcustomer@gmail.com",
		"someone.else@yahoo.com",
		"pro-a1b2c3d4e5f6@demo.test",
		// The local parts on their own, so a partially redacted form
		// still fails.
		"bounce-handler",
		"realcustomer",
		"a1b2c3d4e5f6",
	}
	for _, ev := range allEvents() {
		t.Run(ev.Action(), func(t *testing.T) {
			encoded, err := json.Marshal(ev)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			for _, leak := range leaks {
				if strings.Contains(string(encoded), leak) {
					t.Errorf("%s serialized %q in full: %s", ev.Action(), leak, encoded)
				}
			}
			// Redaction that removed the domain too would be useless:
			// evidence has to stay debuggable. Only events that carry an
			// address are held to it - an extraction failure is about a
			// message, and has no address to keep.
			if carriesAddress(ev) && !strings.Contains(string(encoded), "@") {
				t.Errorf("%s redacted its address away entirely: %s", ev.Action(), encoded)
			}
		})
	}
}

// carriesAddress reports whether an event has any address field.
func carriesAddress(ev ledger.Event) bool {
	typ := reflect.TypeOf(ev)
	for i := range typ.NumField() {
		switch typ.Field(i).Type {
		case reflect.TypeOf(ledger.Addr("")), reflect.TypeOf(ledger.Addrs(nil)):
			return true
		}
	}
	return false
}

// TestNoEventCanCarryASecretField enumerates the fields of every event and
// fails on any name from the classes the plan says must never reach
// evidence. It is a spelling check on purpose: the point is that adding
// such a field means editing this list, in a commit somebody reviews.
func TestNoEventCanCarryASecretField(t *testing.T) {
	forbidden := []string{
		"otp", "code", "password", "passwd", "secret", "token", "seed_secret",
		"totp", "magiclink", "magic_link", "link", "url", "session", "cookie",
		"body", "html", "raw", "credential", "key",
	}
	for _, ev := range allEvents() {
		typ := reflect.TypeOf(ev)
		t.Run(typ.Name(), func(t *testing.T) {
			for i := range typ.NumField() {
				field := typ.Field(i)
				name := strings.ToLower(field.Name)
				tag := strings.ToLower(strings.Split(field.Tag.Get("json"), ",")[0])
				for _, bad := range forbidden {
					if strings.Contains(name, bad) || strings.Contains(tag, bad) {
						t.Errorf("%s.%s (json %q) names a secret-bearing field", typ.Name(), field.Name, tag)
					}
				}
			}
		})
	}
}

// TestNoEventHasAGenericEscapeHatch is the rule that keeps the schema
// meaningful. A map or an `any` field would let a call site put anything
// into evidence while the type still looked reviewed, which is the exact
// leak class this package exists to close.
func TestNoEventHasAGenericEscapeHatch(t *testing.T) {
	for _, ev := range allEvents() {
		typ := reflect.TypeOf(ev)
		t.Run(typ.Name(), func(t *testing.T) {
			for i := range typ.NumField() {
				field := typ.Field(i)
				switch field.Type.Kind() {
				case reflect.Map, reflect.Interface, reflect.Chan, reflect.Func, reflect.UnsafePointer:
					t.Errorf("%s.%s is a %s: evidence fields must be named and concrete",
						typ.Name(), field.Name, field.Type.Kind())
				case reflect.Slice:
					// A slice is allowed only where the redacting Addrs
					// type is used; []string would serialize raw.
					if field.Type != reflect.TypeOf(ledger.Addrs(nil)) {
						t.Errorf("%s.%s is %s, not ledger.Addrs", typ.Name(), field.Name, field.Type)
					}
				case reflect.String:
					// Fine: every remaining field is an id, a state name,
					// or a message this codebase generates.
				default:
					t.Errorf("%s.%s has unexpected kind %s", typ.Name(), field.Name, field.Type.Kind())
				}
			}
		})
	}
}

// TestEveryFieldIsExportedAndTagged catches the field that would silently
// vanish from evidence, which is the failure that looks like nothing at
// all until an incident needs the record.
func TestEveryFieldIsExportedAndTagged(t *testing.T) {
	for _, ev := range allEvents() {
		typ := reflect.TypeOf(ev)
		for i := range typ.NumField() {
			field := typ.Field(i)
			if !field.IsExported() {
				t.Errorf("%s.%s is unexported and will never be recorded", typ.Name(), field.Name)
			}
			if field.Tag.Get("json") == "" {
				t.Errorf("%s.%s carries no json tag", typ.Name(), field.Name)
			}
		}
	}
}

func TestAddrRedaction(t *testing.T) {
	cases := map[ledger.Addr]string{
		"pro-a1b2c3d4e5f6@demo.test": "pro...@demo.test",
		"ab@demo.test":               "ab@demo.test",
		"abc@demo.test":              "abc@demo.test",
		"abcd@demo.test":             "abc...@demo.test",
		"not-an-address":                 "[redacted]",
		"@demo.test":                 "[redacted]",
		"":                               "[redacted]",
		// The domain is what a reader needs to tell an owned address from
		// one that should never have arrived, so it survives whole.
		"someone@gmail.com": "som...@gmail.com",
	}
	for in, want := range cases {
		if got := in.Redacted(); got != want {
			t.Errorf("Addr(%q).Redacted() = %q, want %q", string(in), got, want)
		}
	}
}

// TestEveryDefinedEventIsRegistered is the completeness check the list
// above claims to have.
//
// It was written after a new event reached the package and every rule
// below stayed green, because the rules only ever ran over the list and
// nothing compared the list to the package. An event that escapes this
// check escapes the redaction test, the no-secret-field test and the
// escape-hatch test at once, which is the whole guarantee this package
// exists to give.
//
// Membership is read from the source rather than from a registry the same
// author would have to remember to update: a type in this package with a
// sealed() method is an event, by construction.
func TestEveryDefinedEventIsRegistered(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	fset := token.NewFileSet()
	defined := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "sealed" || fn.Recv == nil || len(fn.Recv.List) != 1 {
				continue
			}
			if ident, ok := fn.Recv.List[0].Type.(*ast.Ident); ok {
				defined[ident.Name] = true
			}
		}
	}
	if len(defined) == 0 {
		t.Fatal("found no event types in the package; the check is not checking anything")
	}

	registered := map[string]bool{}
	for _, ev := range allEvents() {
		registered[reflect.TypeOf(ev).Name()] = true
	}
	for name := range defined {
		if !registered[name] {
			t.Errorf("%s is an event and is not in allEvents, so no rule in this file runs over it", name)
		}
	}
}

// TestActionsAreDistinct keeps two events from writing the same action,
// which would make the trail unreadable by exactly the grep an operator
// reaches for first.
func TestActionsAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, ev := range allEvents() {
		action := ev.Action()
		name := reflect.TypeOf(ev).Name()
		if other, dup := seen[action]; dup {
			t.Errorf("%s and %s both write action %q", other, name, action)
		}
		seen[action] = name
		if action == "" {
			t.Errorf("%s has an empty action", name)
		}
	}
}
