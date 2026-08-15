package apikey

import (
	"errors"
	"strings"
	"testing"
)

// The safe default matters most: a client that does not know about scopes must
// end up with the weakest key, not the strongest.
func TestParseScopesEmptyYieldsReadOnly(t *testing.T) {
	for _, in := range [][]string{nil, {}, {""}, {"  "}} {
		got, err := ParseScopes(in)
		if err != nil {
			t.Fatalf("ParseScopes(%v) errored: %v", in, err)
		}
		if len(got) != 1 || got[0] != ScopeRead {
			t.Errorf("ParseScopes(%v) = %v, want [read]", in, got)
		}
	}
}

// Silently dropping an unrecognised scope would hand back a key weaker than the
// caller believes they hold, and they would only find out at the first refusal.
func TestParseScopesRejectsUnknownScopes(t *testing.T) {
	for _, in := range []string{"admin", "READ", "write:everything", "delete"} {
		if _, err := ParseScopes([]string{in}); err == nil {
			t.Errorf("ParseScopes(%q) was accepted; want rejection", in)
		}
	}

	_, err := ParseScopes([]string{"read", "superuser"})
	if err == nil {
		t.Fatal("a valid scope alongside an invalid one was accepted")
	}
	if !strings.Contains(err.Error(), "superuser") {
		t.Errorf("error does not name the offending scope: %v", err)
	}
}

func TestParseScopesNormalises(t *testing.T) {
	got, err := ParseScopes([]string{" write ", "read", "write", "read"})
	if err != nil {
		t.Fatalf("ParseScopes errored: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ParseScopes returned %v, want two deduplicated scopes", got)
	}
	// Sorted, so a key's scopes render identically everywhere.
	if got[0] != ScopeRead || got[1] != ScopeWrite {
		t.Errorf("ParseScopes = %v, want [read write] in stable order", got)
	}
}

// No implication between scopes: what a key can do is exactly what its row says.
func TestScopesDoNotImplyEachOther(t *testing.T) {
	if Has([]Scope{ScopeWrite}, ScopeRead) {
		t.Error("write implied read")
	}
	if Has([]Scope{ScopeGenerate}, ScopeWrite) {
		t.Error("write:generate implied write")
	}
	if Has([]Scope{ScopeRead}, ScopeWrite) {
		t.Error("read implied write")
	}
}

func TestRequireReturnsATypedError(t *testing.T) {
	err := Require([]Scope{ScopeRead}, ScopeWrite)
	if err == nil {
		t.Fatal("Require allowed a missing scope")
	}

	var scopeErr *ErrInsufficientScope
	if !errors.As(err, &scopeErr) {
		t.Fatalf("Require returned %T, want *ErrInsufficientScope", err)
	}
	if scopeErr.Required != ScopeWrite {
		t.Errorf("Required = %q, want %q", scopeErr.Required, ScopeWrite)
	}
	// The message must say what was needed and what was held, or the caller
	// cannot tell the user how to fix it.
	if !strings.Contains(err.Error(), "write") || !strings.Contains(err.Error(), "read") {
		t.Errorf("error message is not actionable: %v", err)
	}

	if err := Require([]Scope{ScopeRead, ScopeWrite}, ScopeWrite); err != nil {
		t.Errorf("Require refused a held scope: %v", err)
	}
}

func TestRequireOnEmptyScopesFailsClosed(t *testing.T) {
	for _, s := range AllScopes {
		if err := Require(nil, s); err == nil {
			t.Errorf("empty scope set granted %q", s)
		}
	}
}

// A row written by a newer server must not break an older one. Dropping an
// unknown scope narrows access; it never widens it.
func TestScopesFromStringsDropsUnknownValues(t *testing.T) {
	got := ScopesFromStrings([]string{"read", "teleport", "write"})

	if len(got) != 2 || got[0] != ScopeRead || got[1] != ScopeWrite {
		t.Errorf("ScopesFromStrings = %v, want [read write]", got)
	}
}

func TestScopeStringsRoundTrips(t *testing.T) {
	raw := ScopeStrings(AllScopes)
	back := ScopesFromStrings(raw)

	if len(back) != len(AllScopes) {
		t.Fatalf("round trip produced %d scopes, want %d", len(back), len(AllScopes))
	}
	for i := range back {
		if back[i] != AllScopes[i] {
			t.Errorf("round trip changed scope %d: %q -> %q", i, AllScopes[i], back[i])
		}
	}
}

func TestJoinScopesHandlesEmpty(t *testing.T) {
	if got := JoinScopes(nil); got != "none" {
		t.Errorf("JoinScopes(nil) = %q, want %q", got, "none")
	}
	if got := JoinScopes([]Scope{ScopeRead, ScopeWrite}); got != "read, write" {
		t.Errorf("JoinScopes = %q, want %q", got, "read, write")
	}
}

func TestDefaultScopesCannotBeMutatedByCallers(t *testing.T) {
	got, err := ParseScopes(nil)
	if err != nil {
		t.Fatalf("ParseScopes errored: %v", err)
	}
	got[0] = ScopeWrite

	if DefaultScopes[0] != ScopeRead {
		t.Fatal("mutating a returned slice corrupted DefaultScopes; " +
			"a caller could silently widen the default grant")
	}
}

// The default has to survive the round trip through the handler: a client that
// sends no scopes must receive a read-only key, not a fully privileged one.
func TestParseScopesFromProtoInputDefaultsToReadOnly(t *testing.T) {
	// Mirrors req.Msg.GetScopes() for a client that omits the field.
	var fromProto []string

	got, err := ParseScopes(fromProto)
	if err != nil {
		t.Fatalf("ParseScopes errored: %v", err)
	}
	if len(got) != 1 || got[0] != ScopeRead {
		t.Errorf("omitted scopes produced %v, want [read]", got)
	}
	if Has(got, ScopeWrite) || Has(got, ScopeGenerate) {
		t.Error("a key minted without explicit scopes can write or generate")
	}
}

func TestParseScopesAcceptsAnExplicitFullGrant(t *testing.T) {
	got, err := ParseScopes([]string{"read", "write", "write:generate"})
	if err != nil {
		t.Fatalf("ParseScopes errored: %v", err)
	}
	for _, want := range AllScopes {
		if !Has(got, want) {
			t.Errorf("explicit full grant is missing %q", want)
		}
	}
}
