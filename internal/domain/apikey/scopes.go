package apikey

import (
	"fmt"
	"sort"
	"strings"
)

// Scope is a capability granted to an API key.
//
// Keys authenticate as their owning user, so without scopes a key handed to a
// third-party agent for reading also carries the power to rewrite that user's
// itineraries and spend their generation quota. Scopes make the grant explicit
// and narrowable at mint time.
type Scope string

const (
	// ScopeRead permits retrieval and listing: search, details, and reading the
	// caller's own saved lists, favorites and itineraries.
	ScopeRead Scope = "read"

	// ScopeWrite permits mutating the caller's own saved data.
	ScopeWrite Scope = "write"

	// ScopeGenerate permits operations that spend the daily LLM quota. Kept
	// separate from ScopeWrite because it costs money per call: "may save a
	// favorite" should not imply "may run generations against my balance".
	ScopeGenerate Scope = "write:generate"
)

// DefaultScopes is what a key gets when the caller does not choose. Read-only:
// the safe grant is the one that cannot change or spend anything.
var DefaultScopes = []Scope{ScopeRead}

// AllScopes is the full set, used to validate input and to backfill keys minted
// before scopes existed.
var AllScopes = []Scope{ScopeRead, ScopeWrite, ScopeGenerate}

// ErrInsufficientScope is returned when a key lacks the scope a tool requires.
// Callers translate it into the transport's permission-denied representation.
type ErrInsufficientScope struct {
	Required Scope
	Held     []Scope
}

func (e *ErrInsufficientScope) Error() string {
	return fmt.Sprintf("api key lacks the %q scope (holds: %s)",
		e.Required, JoinScopes(e.Held))
}

// Valid reports whether s is a scope this server recognises.
func (s Scope) Valid() bool {
	for _, known := range AllScopes {
		if s == known {
			return true
		}
	}
	return false
}

// Has reports whether the granted set includes required.
//
// No implication between scopes: holding "write" does not confer "read". A key
// meant for both must be minted with both, so what a key can do is always
// exactly what its row says.
func Has(granted []Scope, required Scope) bool {
	for _, s := range granted {
		if s == required {
			return true
		}
	}
	return false
}

// Require returns an *ErrInsufficientScope unless granted includes required.
func Require(granted []Scope, required Scope) error {
	if Has(granted, required) {
		return nil
	}
	return &ErrInsufficientScope{Required: required, Held: granted}
}

// ParseScopes validates and normalises caller-supplied scope strings.
//
// Empty input yields DefaultScopes rather than an error: a client that does not
// know about scopes gets the safe grant instead of a failure. Unknown scopes are
// rejected loudly — silently dropping one would hand back a key weaker than the
// caller believes they hold.
func ParseScopes(raw []string) ([]Scope, error) {
	if len(raw) == 0 {
		return append([]Scope(nil), DefaultScopes...), nil
	}

	seen := make(map[Scope]struct{}, len(raw))
	out := make([]Scope, 0, len(raw))
	for _, r := range raw {
		s := Scope(strings.TrimSpace(r))
		if s == "" {
			continue
		}
		if !s.Valid() {
			return nil, fmt.Errorf("unknown scope %q; valid scopes are %s",
				r, JoinScopes(AllScopes))
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return append([]Scope(nil), DefaultScopes...), nil
	}

	// Stable order so a key's scopes render identically everywhere.
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// ScopeStrings converts scopes for storage or transport.
func ScopeStrings(scopes []Scope) []string {
	out := make([]string, len(scopes))
	for i, s := range scopes {
		out[i] = string(s)
	}
	return out
}

// ScopesFromStrings converts stored values back, dropping anything this build
// does not recognise. A row written by a newer server must not break an older
// one; the effect of dropping is narrower access, never wider.
func ScopesFromStrings(raw []string) []Scope {
	out := make([]Scope, 0, len(raw))
	for _, r := range raw {
		if s := Scope(r); s.Valid() {
			out = append(out, s)
		}
	}
	return out
}

// JoinScopes renders scopes for an error message or log line.
func JoinScopes(scopes []Scope) string {
	if len(scopes) == 0 {
		return "none"
	}
	return strings.Join(ScopeStrings(scopes), ", ")
}
