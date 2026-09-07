package ai

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/FACorreiaa/loci-connect-api/pkg/ai/providers"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/gemini"
	"github.com/FACorreiaa/loci-connect-api/pkg/openrouter"
)

// UserSpec is one person's credential, ready to become a client.
//
// The fields are plain strings rather than an aicreds type on purpose: this
// package is below internal/domain, and taking the domain type here would put
// a cycle between the credential store and the client it builds.
type UserSpec struct {
	// Provider must name an entry in providers.Catalog.
	Provider string

	// Model is already resolved — the user's choice or the catalogue default.
	Model string

	// BaseURL is set only for providers whose address the user supplies, and
	// must already have been through providers.ParseGatewayURL.
	BaseURL string

	// APIKey is plaintext. Do not log this struct.
	APIKey string

	// HTTPClient is shared so a per-user provider gets connection reuse rather
	// than a TLS handshake per request. For a user-supplied address it is
	// wrapped in providers.GatewayHTTPClient before use.
	HTTPClient *http.Client

	Logger *slog.Logger
}

// NewUserChatClient builds a chat client from a credential a person brought
// themselves.
//
// Deliberately not registered into the chain built by NewChatClient. That one
// is constructed once at startup and read without locking; a per-user client
// stored into it would be a data race, and one user's key would answer another
// user's request. This hands the client back instead, for the caller to use and
// drop.
func NewUserChatClient(ctx context.Context, spec UserSpec) (generativeAI.ChatClient, error) {
	entry, ok := providers.ByName(spec.Provider)
	if !ok {
		return nil, fmt.Errorf("ai: %q is not a provider a key can be brought for", spec.Provider)
	}

	model := spec.Model
	if model == "" {
		model = entry.DefaultModel
	}

	logger := spec.Logger
	if logger == nil {
		logger = slog.Default()
	}

	baseURL := entry.BaseURL
	httpClient := spec.HTTPClient
	if entry.RequiresBaseURL {
		// Validated again here rather than trusted from the caller: this is
		// the last point before the address is dialled, and the value came
		// out of a database row.
		parsed, err := providers.ParseGatewayURL(spec.BaseURL)
		if err != nil {
			// Not wrapped with the spec: it holds the key.
			return nil, fmt.Errorf("ai: cannot build a %s client for this credential", entry.Name)
		}
		baseURL = parsed
		// Re-resolves and re-checks the target at dial time, so a name that
		// pointed somewhere public when it was saved cannot be rebound onto
		// loopback or cloud metadata afterwards.
		httpClient = providers.GatewayHTTPClient(spec.HTTPClient)
	}

	var (
		client generativeAI.ChatClient
		err    error
	)
	if entry.Name == config.AIProviderGemini {
		// Gemini is not an OpenAI-dialect backend and is reached through its
		// own SDK, which takes its settings as an AIConfig.
		client, err = gemini.NewChatClient(ctx, config.AIConfig{
			Provider: config.AIProviderGemini,
			APIKey:   spec.APIKey,
			Model:    model,
		}, logger)
	} else {
		client, err = openrouter.NewCompatibleChatClient(openrouter.Options{
			Name:       entry.Name,
			BaseURL:    baseURL,
			APIKey:     spec.APIKey,
			Model:      model,
			HTTPClient: httpClient,
			Logger:     logger,
		})
	}
	if err != nil {
		// The underlying error can carry the request that failed. Report the
		// provider and nothing else.
		return nil, fmt.Errorf("ai: cannot build a %s client for this credential", entry.Name)
	}

	// Metered like every other link, so tokens are attributed to the model that
	// answered. The counters do not yet distinguish a user's own spend from
	// ours; they measure usage, and usage is real either way.
	return newMetered(client, recordPrometheus), nil
}
