package ai

import (
	"strings"
	"testing"
)

func TestNewUserChatClientBuildsEachCatalogueBackend(t *testing.T) {
	for _, tc := range []struct {
		provider string
		baseURL  string
	}{
		{provider: "openrouter"},
		{provider: "openai"},
		{provider: "xai"},
		{provider: "nvidia"},
		{provider: "gemini"},
		{provider: "hermes", baseURL: "http://hermes-vps-2.tail562587.ts.net:8642/v1"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			client, err := NewUserChatClient(t.Context(), UserSpec{
				Provider: tc.provider, APIKey: "test-key", BaseURL: tc.baseURL,
			})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if client.Model() == "" {
				t.Error("the client was built with no model; the catalogue default did not apply")
			}
		})
	}
}

func TestNewUserChatClientRefusesWhatItCannotDial(t *testing.T) {
	for name, spec := range map[string]UserSpec{
		"unknown provider":   {Provider: "anthropic", APIKey: "k"},
		"empty provider":     {Provider: "", APIKey: "k"},
		"hermes without url": {Provider: "hermes", APIKey: "k"},
		"hermes on loopback": {Provider: "hermes", APIKey: "k", BaseURL: "http://127.0.0.1:8642/v1"},
		"hermes on metadata": {Provider: "hermes", APIKey: "k", BaseURL: "http://169.254.169.254/v1"},
		"no key":             {Provider: "openrouter"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewUserChatClient(t.Context(), spec); err == nil {
				t.Fatal("a client was built")
			}
		})
	}
}

// Errors from this path are logged and shown to the user. The key must not
// travel with them.
func TestBuildErrorsNeverNameTheKey(t *testing.T) {
	const key = "sk-or-v1-must-never-appear-in-an-error"

	_, err := NewUserChatClient(t.Context(), UserSpec{
		Provider: "hermes", APIKey: key, BaseURL: "http://127.0.0.1:8642/v1",
	})
	if err == nil {
		t.Fatal("no error")
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("the error names the key: %q", err)
	}
}

// The user's model overrides the catalogue's default; an empty one falls back.
func TestTheUsersModelWins(t *testing.T) {
	client, err := NewUserChatClient(t.Context(), UserSpec{
		Provider: "openrouter", APIKey: "k", Model: "anthropic/claude-opus-4",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := client.Model(); got != "anthropic/claude-opus-4" {
		t.Errorf("Model = %q, want the user's choice", got)
	}
}
