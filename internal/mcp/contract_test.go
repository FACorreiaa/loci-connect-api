package mcp

import (
	"context"
	"sort"
	"testing"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/apikey"
)

// The frozen tool set. This list is the contract documented in
// docs/mcp-contract.md; changing it here without changing the document (and
// meaning to) is the mistake this test exists to catch.
var contractTools = map[string]apikey.Scope{
	// Read-only.
	"status":           apikey.ScopeRead,
	"search_pois":      apikey.ScopeRead,
	"get_poi_details":  apikey.ScopeRead,
	"find_nearby":      apikey.ScopeRead,
	"list_itineraries": apikey.ScopeRead,
	"get_itinerary":    apikey.ScopeRead,
	"list_user_lists":  apikey.ScopeRead,
	"get_list":         apikey.ScopeRead,
	"list_favorites":   apikey.ScopeRead,

	// Mutating.
	"update_itinerary": apikey.ScopeWrite,
	"add_poi_to_list":  apikey.ScopeWrite,
	"add_favorite":     apikey.ScopeWrite,

	// Generating.
	"plan_itinerary": apikey.ScopeGenerate,
}

func TestToolSetMatchesTheContract(t *testing.T) {
	got := allToolNames()

	if len(got) != len(contractTools) {
		t.Errorf("server exposes %d tools, contract documents %d", len(got), len(contractTools))
	}

	seen := make(map[string]bool, len(got))
	for _, name := range got {
		if seen[name] {
			t.Errorf("tool %q is registered twice", name)
		}
		seen[name] = true
		if _, documented := contractTools[name]; !documented {
			t.Errorf("tool %q is exposed but not in the contract; document it in "+
				"docs/mcp-contract.md and classify it in scopes.go", name)
		}
	}
	for name := range contractTools {
		if !seen[name] {
			t.Errorf("tool %q is in the contract but not exposed", name)
		}
	}
}

func TestEveryToolIsClassifiedAsDocumented(t *testing.T) {
	for name, want := range contractTools {
		if got := toolScope(name); got != want {
			t.Errorf("tool %q requires scope %q, contract says %q", name, got, want)
		}
	}
}

// A tool must appear in exactly one classification list. Landing in two would
// make its required scope depend on list ordering.
func TestToolClassificationsDoNotOverlap(t *testing.T) {
	counts := make(map[string]int)
	for _, group := range [][]string{readOnlyTools, mutatingTools, generatingTools} {
		for _, name := range group {
			counts[name]++
		}
	}
	for name, n := range counts {
		if n != 1 {
			t.Errorf("tool %q appears in %d classification lists, want exactly 1", name, n)
		}
	}
}

// The safety property behind the whole scope system: a read-only key must not
// reach anything that changes the owner's data or spends their quota.
func TestReadOnlyKeyCannotReachWriteTools(t *testing.T) {
	ctx := withScopes(context.Background(), []apikey.Scope{apikey.ScopeRead})

	for _, name := range append(append([]string{}, mutatingTools...), generatingTools...) {
		if err := requireScope(ctx, toolScope(name)); err == nil {
			t.Errorf("a read-only key was allowed to call %q", name)
		}
	}
	for _, name := range readOnlyTools {
		if err := requireScope(ctx, toolScope(name)); err != nil {
			t.Errorf("a read-only key was refused the read tool %q: %v", name, err)
		}
	}
}

// Write access must not imply the right to spend money.
func TestWriteScopeDoesNotGrantGeneration(t *testing.T) {
	ctx := withScopes(context.Background(), []apikey.Scope{apikey.ScopeRead, apikey.ScopeWrite})

	for _, name := range mutatingTools {
		if err := requireScope(ctx, toolScope(name)); err != nil {
			t.Errorf("a write key was refused %q: %v", name, err)
		}
	}
	for _, name := range generatingTools {
		if err := requireScope(ctx, toolScope(name)); err == nil {
			t.Errorf("a key without write:generate was allowed to call %q", name)
		}
	}
}

// Fail closed. A context that never passed through authMiddleware carries no
// scopes and must be refused everything, including reads.
func TestUnscopedContextIsRefusedEverything(t *testing.T) {
	ctx := context.Background()

	for name := range contractTools {
		if err := requireScope(ctx, toolScope(name)); err == nil {
			t.Errorf("tool %q was allowed with no scopes in context", name)
		}
	}
}

func TestFullyScopedKeyReachesEverything(t *testing.T) {
	ctx := withScopes(context.Background(), apikey.AllScopes)

	for name := range contractTools {
		if err := requireScope(ctx, toolScope(name)); err != nil {
			t.Errorf("a fully scoped key was refused %q: %v", name, err)
		}
	}
}

// An unclassified tool defaults to requiring read, which is the safe direction
// for a reader but wrong for a writer — so this documents the default and pairs
// with TestToolSetMatchesTheContract, which catches anything left unclassified.
func TestUnknownToolDefaultsToRead(t *testing.T) {
	if got := toolScope("some_tool_that_does_not_exist"); got != apikey.ScopeRead {
		t.Errorf("unknown tool defaulted to %q, want %q", got, apikey.ScopeRead)
	}
}

func TestAllToolNamesIsStable(t *testing.T) {
	first := allToolNames()
	second := allToolNames()

	if len(first) != len(second) {
		t.Fatalf("allToolNames returned %d then %d entries", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("allToolNames order varied between calls at index %d", i)
		}
	}

	// Read-only tools come first, so a reader scanning the list sees the safe
	// surface before the dangerous one.
	if len(first) < len(readOnlyTools) {
		t.Fatal("allToolNames is shorter than the read-only list")
	}
	sorted := append([]string{}, readOnlyTools...)
	sort.Strings(sorted)
	gotHead := append([]string{}, first[:len(readOnlyTools)]...)
	sort.Strings(gotHead)
	for i := range sorted {
		if sorted[i] != gotHead[i] {
			t.Errorf("read-only tools are not listed first: got %v", first[:len(readOnlyTools)])
			break
		}
	}
}
