package apikey

import (
	"encoding/json"
	"strings"
	"testing"
)

func testWriter() *SetupWriter {
	return NewSetupWriter(
		"https://api.loci.example/", "/mcp",
		[]string{"search_pois", "get_poi_details", "list_favorites"},
		[]string{"add_favorite"},
		[]string{"plan_itinerary"},
	)
}

func TestTheEndpointIsBuiltOnce(t *testing.T) {
	w := testWriter()
	if w.Endpoint() != "https://api.loci.example/mcp" {
		t.Fatalf("endpoint = %q; a trailing slash on the base URL would double it", w.Endpoint())
	}
	for _, kind := range ClientKinds {
		if got := w.Preview(kind).Endpoint; got != w.Endpoint() {
			t.Errorf("%s: endpoint = %q", kind, got)
		}
	}
}

// The preview exists so nobody has to mint a key to see what setup looks like.
// A key leaking into it would defeat that and leave a live credential in a page
// that is opened repeatedly.
func TestThePreviewCarriesNoRealCredential(t *testing.T) {
	for _, kind := range ClientKinds {
		t.Run(string(kind), func(t *testing.T) {
			setup := testWriter().Preview(kind)
			blob := setup.Config + setup.Safe + setup.Export + setup.Prompt

			if !strings.Contains(blob, PlaceholderToken) {
				t.Error("the placeholder does not appear anywhere; there is nothing to replace")
			}
			if strings.Contains(blob, "loci_sk_") {
				t.Error("the preview contains something shaped like a real key")
			}
		})
	}
}

// A convincing placeholder is how somebody pastes the preview into a config and
// spends an hour on a key that never existed.
func TestThePlaceholderCannotBeMistakenForAKey(t *testing.T) {
	if strings.HasPrefix(PlaceholderToken, KeyPrefix) {
		t.Fatal("the placeholder is shaped like a real key")
	}
	if !strings.Contains(strings.ToUpper(PlaceholderToken), "PASTE") {
		t.Error("the placeholder does not say what it is")
	}
}

func TestTheIssuedInstructionsCarryTheKey(t *testing.T) {
	const token = "loci_sk_realtoken0123456789"

	for _, kind := range ClientKinds {
		t.Run(string(kind), func(t *testing.T) {
			setup := testWriter().Instructions(kind, token)

			// Codex never writes the key into its file — it reads the
			// environment — so for that client the key is in the export line.
			blob := setup.Config + setup.Safe + setup.Export
			if !strings.Contains(blob, token) {
				t.Error("the key appears in none of the copyable blocks")
			}
			if !strings.Contains(setup.Prompt, token) {
				t.Error("the prompt does not carry the key")
			}
		})
	}
}

// .mcp.json lives in a project root and is committed routinely. Offering only
// the literal form is how a key reaches a public repository.
func TestTheCommittableFileNeverContainsTheKey(t *testing.T) {
	const token = "loci_sk_realtoken0123456789"

	for _, kind := range []ClientKind{ClientClaudeCode, ClientCodex} {
		t.Run(string(kind), func(t *testing.T) {
			setup := testWriter().Instructions(kind, token)

			if setup.Safe == "" {
				t.Fatal("no git-safe form is offered for a client configured by file")
			}
			if strings.Contains(setup.Safe, token) {
				t.Error("the git-safe configuration contains the key literally")
			}
			if !strings.Contains(setup.Safe, TokenEnvVar) {
				t.Errorf("the git-safe configuration does not reference %s", TokenEnvVar)
			}
			if !strings.Contains(setup.Export, token) {
				t.Error("no export line sets the variable the config reads")
			}
			if setup.SafeNote == "" {
				t.Error("no note explaining why a second form exists")
			}
		})
	}
}

// Codex reads the key from the environment and has no inline form, so its
// primary command must not contain one either.
func TestCodexNeverTakesTheKeyInline(t *testing.T) {
	const token = "loci_sk_realtoken0123456789"
	setup := testWriter().Instructions(ClientCodex, token)

	if strings.Contains(setup.Config, token) {
		t.Error("the codex command carries the key inline; it reads the environment instead")
	}
	if !strings.Contains(setup.Config, TokenEnvVar) {
		t.Error("the codex command does not name the environment variable")
	}
}

func TestTheClaudeCodeSafeFormIsValidJSON(t *testing.T) {
	setup := testWriter().Preview(ClientClaudeCode)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(setup.Safe), &parsed); err != nil {
		t.Fatalf(".mcp.json is not valid JSON: %v", err)
	}
	servers, ok := parsed["mcpServers"].(map[string]any)
	if !ok || servers["loci"] == nil {
		t.Fatalf("the snippet does not register a server called loci: %v", parsed)
	}
}

// An agent that verifies a new connection by planning an itinerary has spent
// the owner's daily quota to prove it could.
func TestThePromptNamesAReadOnlyCheckAndWarnsOffTheRest(t *testing.T) {
	prompt := testWriter().Preview(ClientOther).Prompt

	for _, tool := range []string{"search_pois", "get_poi_details", "list_favorites"} {
		if !strings.Contains(prompt, tool) {
			t.Errorf("the prompt does not name the read-only tool %s", tool)
		}
	}
	for _, tool := range []string{"add_favorite", "plan_itinerary"} {
		if !strings.Contains(prompt, tool) {
			t.Errorf("the prompt does not warn against %s", tool)
		}
	}
	if !strings.Contains(prompt, "never in the URL") {
		t.Error("the prompt does not say to keep the credential out of the URL")
	}
}

// An unrecognised kind must still produce something that works, since the
// value can reach here from a stored row.
func TestAnUnknownKindFallsBackToConnectionDetails(t *testing.T) {
	setup := testWriter().Preview(ClientKind("emacs"))

	if setup.Kind != ClientOther {
		t.Errorf("Kind = %q, want %q", setup.Kind, ClientOther)
	}
	if !strings.Contains(setup.Config, "streamable HTTP") {
		t.Error("the fallback does not describe the transport")
	}
	if !strings.Contains(setup.Config, setup.Endpoint) {
		t.Error("the fallback does not give the URL")
	}
}

func TestParseClientKind(t *testing.T) {
	if got, err := ParseClientKind(""); err != nil || got != ClientOther {
		t.Errorf("empty parsed to (%q, %v), want other and no error", got, err)
	}
	for _, kind := range ClientKinds {
		if got, err := ParseClientKind(string(kind)); err != nil || got != kind {
			t.Errorf("%q parsed to (%q, %v)", kind, got, err)
		}
	}
	// Silently rewriting this to "other" would tell the caller their choice was
	// accepted when it was discarded.
	if _, err := ParseClientKind("emacs"); err == nil {
		t.Error("an unrecognised kind was accepted")
	}
}
