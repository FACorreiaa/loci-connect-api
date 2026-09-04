package mcp

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/apikey"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
)

// scopesKey is the context key carrying the presenting key's granted scopes.
type scopesContextKey struct{}

// withScopes attaches the authenticated key's scopes to the request context.
func withScopes(ctx context.Context, scopes []apikey.Scope) context.Context {
	return context.WithValue(ctx, scopesContextKey{}, scopes)
}

// scopesFromContext returns the scopes granted to the presenting key.
//
// A context with no scopes yields none, and every scope check therefore fails
// closed. That matters: a code path that reaches a tool without going through
// authMiddleware must be denied, not waved through.
func scopesFromContext(ctx context.Context) []apikey.Scope {
	scopes, _ := ctx.Value(scopesContextKey{}).([]apikey.Scope)
	return scopes
}

// requireScope denies the call unless the presenting key holds required.
//
// Returns an MCP tool error rather than a transport error: the call
// authenticated correctly, it simply asked for something this key may not do,
// and the calling agent should see that as a refused tool rather than a broken
// connection.
func requireScope(ctx context.Context, required apikey.Scope) error {
	if err := apikey.Require(scopesFromContext(ctx), required); err != nil {
		return toolError(err)
	}
	return nil
}

// readOnly and mutating classify every tool in the server.
//
// The distinction is stated here rather than left implicit in each handler,
// because "which of these tools can change my data?" is the first question
// anyone handing out an API key should be able to answer — and until now the
// only way to answer it was to read all four tool files.
var (
	readOnlyTools = []string{
		"status",
		"search_pois",
		"get_poi_details",
		"find_nearby",
		"list_itineraries",
		"get_itinerary",
		"list_user_lists",
		"get_list",
		"list_favorites",
	}

	// mutatingTools change the caller's stored data. Each requires ScopeWrite.
	mutatingTools = []string{
		"update_itinerary",
		"add_poi_to_list",
		"add_favorite",
	}

	// generatingTools spend the daily LLM quota and require ScopeGenerate.
	generatingTools = []string{
		"plan_itinerary",
	}
)

// toolScope reports the scope a named tool requires.
func toolScope(name string) apikey.Scope {
	for _, t := range mutatingTools {
		if t == name {
			return apikey.ScopeWrite
		}
	}
	for _, t := range generatingTools {
		if t == name {
			return apikey.ScopeGenerate
		}
	}
	return apikey.ScopeRead
}

// allToolNames is every tool the server registers, read-only first. The MCP
// contract test asserts the running server exposes exactly this set, so adding
// a tool without classifying it fails the build rather than shipping an
// unclassified capability.
func allToolNames() []string {
	names := make([]string, 0, len(readOnlyTools)+len(mutatingTools)+len(generatingTools))
	names = append(names, readOnlyTools...)
	names = append(names, mutatingTools...)
	names = append(names, generatingTools...)
	return names
}

// Outcomes recorded for a tool call. Bounded set, safe as a metric label.
const (
	outcomeOK     = "ok"
	outcomeDenied = "denied"
	outcomeError  = "error"

	// mcpToolCallMsg is the log message Gate 2's distinct-user count is built
	// on. Changing it breaks that query.
	mcpToolCallMsg = "mcp tool call"
)

// guardTool wraps a tool handler with its scope requirement and records the
// call.
//
// Every tool goes through here and the contract test keeps it that way, so this
// is the one place that sees every invocation. The counter carries volume by
// tool and outcome; the log line carries the caller, because a user id in a
// Prometheus label would be unbounded cardinality.
func guardTool[In, Out any](
	deps Deps,
	name string,
	handler func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
) func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error) {
	required := toolScope(name)
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		capture := func(outcome string) {
			userID, _ := interceptors.GetUserIDFromContext(ctx)
			deps.Analytics.Capture(userID, "mcp_tool_called", map[string]any{
				"tool":    name,
				"outcome": outcome,
			})
		}

		record := func(outcome string) {
			observability.MCPToolCallsTotal.WithLabelValues(name, outcome).Inc()
			capture(outcome)
			if deps.Logger == nil {
				return
			}
			userID, _ := interceptors.GetUserIDFromContext(ctx)
			deps.Logger.InfoContext(ctx, mcpToolCallMsg,
				slog.String("tool", name),
				slog.String("user_id", userID),
				slog.String("outcome", outcome))
		}

		if err := requireScope(ctx, required); err != nil {
			record(outcomeDenied)
			var zero Out
			return nil, zero, err
		}
		result, out, err := handler(ctx, req, in)
		if err != nil {
			record(outcomeError)
			return result, out, err
		}
		record(outcomeOK)
		return result, out, nil
	}
}
