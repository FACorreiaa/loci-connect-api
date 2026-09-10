package providers

import (
	"strings"
	"testing"
)

// The settings page renders the catalogue directly, so a missing label or hint
// is a blank space in the UI rather than a compile error.
func TestEveryEntryIsRenderable(t *testing.T) {
	for _, p := range Catalog {
		t.Run(p.Name, func(t *testing.T) {
			if p.Name == "" || p.Label == "" {
				t.Error("an entry needs both a stored name and a written label")
			}
			if p.DefaultModel == "" {
				t.Error("no default model: leaving the model blank would build a client with none")
			}
			if p.KeyHint == "" {
				t.Error("no key hint: the field would have no placeholder to check a paste against")
			}
		})
	}
}

func TestNamesAreUniqueAndFindable(t *testing.T) {
	seen := make(map[string]bool, len(Catalog))
	for _, p := range Catalog {
		if seen[p.Name] {
			t.Fatalf("%q appears twice; ByName would return whichever came first", p.Name)
		}
		seen[p.Name] = true

		found, ok := ByName(p.Name)
		if !ok || found.Name != p.Name {
			t.Fatalf("ByName(%q) did not return its own entry", p.Name)
		}
	}

	if _, ok := ByName("anthropic"); ok {
		t.Error("anthropic is deliberately absent; its native API is a different dialect")
	}
	if _, ok := ByName(""); ok {
		t.Error("the empty provider resolved; an unset column must not dial anything")
	}
}

// A base URL is either ours to hardcode or the user's to supply. An entry with
// neither cannot be dialled, and an entry with both would silently ignore one.
func TestEveryEntryHasExactlyOneSourceOfItsAddress(t *testing.T) {
	for _, p := range Catalog {
		t.Run(p.Name, func(t *testing.T) {
			switch {
			case p.RequiresBaseURL && p.BaseURL != "":
				t.Error("the user supplies this address, so the catalogue must not also name one")
			case p.Name == "gemini":
				if p.BaseURL != "" {
					t.Error("gemini is reached through its own SDK, not a base URL")
				}
			case !p.RequiresBaseURL && p.BaseURL == "":
				t.Error("no address, and the user is not asked for one")
			case !p.RequiresBaseURL && !strings.HasPrefix(p.BaseURL, "https://"):
				t.Errorf("BaseURL = %q; a hardcoded address has no reason not to be https", p.BaseURL)
			}
		})
	}
}

// Hermes is the only backend Loci dials at an address it did not choose, which
// is why ParseGatewayURL exists. A second one added without noticing would
// bypass that fence.
func TestOnlyHermesTakesAUserSuppliedAddress(t *testing.T) {
	var withURL []string
	for _, p := range Catalog {
		if p.RequiresBaseURL {
			withURL = append(withURL, p.Name)
		}
	}
	if len(withURL) != 1 || withURL[0] != "hermes" {
		t.Fatalf("providers taking a user-supplied address = %v, want [hermes]; "+
			"any new one must be fenced by ParseGatewayURL", withURL)
	}
}

// VerifyPath is joined onto a base URL. An entry carrying one with no address
// to join it to would build a nonsense request the moment somebody saved a key.
func TestVerifyPathHasSomethingToJoinOnto(t *testing.T) {
	for _, p := range Catalog {
		if p.VerifyPath == "" {
			continue
		}
		t.Run(p.Name, func(t *testing.T) {
			if p.BaseURL == "" && !p.RequiresBaseURL {
				t.Error("a verify path with no base URL to resolve it against")
			}
			if !strings.HasPrefix(p.VerifyPath, "/") {
				t.Errorf("VerifyPath = %q, want a rooted path", p.VerifyPath)
			}
		})
	}
}

// A blank model field must not commit the user to premium rates or to whatever
// a router picks. The OpenRouter default is where that was true: it named a
// Claude model, and "openrouter/auto" would be the same mistake in one word.
func TestNoDefaultIsARouterOrAPremiumModel(t *testing.T) {
	for _, p := range Catalog {
		if p.DefaultModel == "openrouter/auto" {
			t.Errorf("%s: default model is the router, which can pick any model", p.Name)
		}
		if strings.HasPrefix(p.DefaultModel, "anthropic/") {
			t.Errorf("%s: default model %q is a Claude model; that is a choice the user should make", p.Name, p.DefaultModel)
		}
	}

	entry, _ := ByName("openrouter")
	if entry.DefaultModel != "deepseek/deepseek-v4-flash" {
		t.Errorf("openrouter default = %q", entry.DefaultModel)
	}
}
