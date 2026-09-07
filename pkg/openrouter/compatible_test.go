package openrouter

import (
	"net/http"
	"strings"
	"testing"
)

func TestCompatibleClientDefaultsToOpenRouter(t *testing.T) {
	c, err := NewCompatibleChatClient(Options{APIKey: "k", Model: "m"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if c.name != "openrouter" {
		t.Errorf("name = %q, want openrouter", c.name)
	}
	if c.baseURL != defaultBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, defaultBaseURL)
	}
}

// A gateway URL arrives already normalised, but a trailing slash would produce
// "…/v1//chat/completions", which some backends 404.
func TestCompatibleClientTrimsATrailingSlash(t *testing.T) {
	c, err := NewCompatibleChatClient(Options{
		Name: "hermes", APIKey: "k", Model: "hermes-3",
		BaseURL: "http://hermes-vps-2.tail562587.ts.net:8642/v1/",
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if c.baseURL != "http://hermes-vps-2.tail562587.ts.net:8642/v1" {
		t.Errorf("baseURL = %q", c.baseURL)
	}
}

// Reporting a user's own gateway failing as an OpenRouter failure would send
// somebody to look at the wrong service.
func TestErrorsNameTheBackendTheyCameFrom(t *testing.T) {
	c, err := NewCompatibleChatClient(Options{Name: "hermes", APIKey: "k", Model: "hermes-3"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	_, err = c.StartChatSession(t.Context(), nil)
	if err == nil {
		t.Fatal("StartChatSession returned no error")
	}
	if !strings.Contains(err.Error(), "hermes") {
		t.Errorf("error = %q, want it to name hermes", err)
	}

	if _, err := NewCompatibleChatClient(Options{Name: "xai", Model: "grok-4"}); err == nil ||
		!strings.Contains(err.Error(), "xai") {
		t.Errorf("missing-key error = %v, want it to name xai", err)
	}
}

// Attribution headers are OpenRouter's. Sending a referrer identifying Loci to
// a machine on somebody's tailnet is not ours to do.
func TestAttributionHeadersGoOnlyToOpenRouter(t *testing.T) {
	for name, wantHeaders := range map[string]bool{
		"openrouter": true,
		"hermes":     false,
		"xai":        false,
	} {
		t.Run(name, func(t *testing.T) {
			c, err := NewCompatibleChatClient(Options{Name: name, APIKey: "k", Model: "m"})
			if err != nil {
				t.Fatalf("new: %v", err)
			}

			req, err := http.NewRequest(http.MethodPost, "https://example.invalid/v1/chat/completions", nil)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			c.setHeaders(req)

			if got := req.Header.Get("HTTP-Referer") != ""; got != wantHeaders {
				t.Errorf("HTTP-Referer present = %v, want %v", got, wantHeaders)
			}
			if req.Header.Get("Authorization") != "Bearer k" {
				t.Error("the key is not sent as a bearer token")
			}
		})
	}
}
