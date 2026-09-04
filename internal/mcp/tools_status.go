package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/apikey"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/retrieval"
)

// statusInput is empty: status describes the server and the calling key, and
// takes no arguments.
type statusInput struct{}

type statusOutput struct {
	Server  string `json:"server"`
	Version string `json:"version"`

	// Scopes is what the presenting key may do, and Tools lists what it may
	// therefore call. An agent that checks this first stops burning turns on
	// tools it will be refused.
	Scopes        []string `json:"scopes"`
	AllowedTools  []string `json:"allowed_tools"`
	ReadOnlyTools []string `json:"read_only_tools"`
	WriteTools    []string `json:"write_tools"`
	GenerateTools []string `json:"generate_tools"`

	// Limits are the bounds every list-returning tool enforces, so a caller can
	// size its own paging instead of discovering the cap by truncation.
	Limits statusLimits `json:"limits"`

	// Grounding tells an agent what Loci's results are and are not. Loci returns
	// stored rows with stable identifiers, and every result carries why it
	// surfaced — an agent relaying them can cite them rather than launder them
	// into its own assertions.
	Grounding statusGrounding `json:"grounding"`
}

type statusLimits struct {
	MaxResults          int `json:"max_results"`
	MaxDescriptionChars int `json:"max_description_chars"`
	MaxQueryChars       int `json:"max_query_chars"`
	RequestsPerMinute   int `json:"requests_per_minute"`
}

type statusGrounding struct {
	StableIDs    bool     `json:"stable_ids"`
	MatchReasons []string `json:"match_reasons"`
	Note         string   `json:"note"`
}

func registerStatusTools(server *mcp.Server, deps Deps) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "status",
		Description: "Report what this API key may do, the bounds every tool enforces, " +
			"and how Loci's results are grounded. Call this first to avoid attempting " +
			"tools the key cannot use.",
	}, guardTool(deps, "status", func(ctx context.Context, _ *mcp.CallToolRequest, _ statusInput) (*mcp.CallToolResult, statusOutput, error) {
		granted := scopesFromContext(ctx)

		allowed := make([]string, 0, len(allToolNames()))
		for _, name := range allToolNames() {
			if apikey.Has(granted, toolScope(name)) {
				allowed = append(allowed, name)
			}
		}

		return nil, statusOutput{
			Server:        serverName,
			Version:       Version,
			Scopes:        apikey.ScopeStrings(granted),
			AllowedTools:  allowed,
			ReadOnlyTools: readOnlyTools,
			WriteTools:    mutatingTools,
			GenerateTools: generatingTools,
			Limits: statusLimits{
				MaxResults:          maxToolResults,
				MaxDescriptionChars: descriptionLimit,
				MaxQueryChars:       retrieval.MaxQueryChars,
				RequestsPerMinute:   requestsPerMinute,
			},
			Grounding: statusGrounding{
				StableIDs: true,
				MatchReasons: []string{
					string(retrieval.MatchLexical),
					string(retrieval.MatchSemantic),
					string(retrieval.MatchBoth),
					string(retrieval.MatchNearby),
				},
				Note: "Results are stored rows with stable ids. Each carries a " +
					"match_reason explaining why it surfaced, and a source " +
					"describing where its data came from. Cite the id rather than " +
					"restating the content as your own.",
			},
		}, nil
	}))
}
