package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/apikey"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

type toolCall struct {
	Tool    string `json:"tool"`
	UserID  string `json:"user_id"`
	Outcome string `json:"outcome"`
	Msg     string `json:"msg"`
}

func callerContext(userID string, scopes ...apikey.Scope) context.Context {
	ctx := interceptors.ContextWithClaims(context.Background(), &interceptors.Claims{UserID: userID})
	return withScopes(ctx, scopes)
}

func decodeCalls(t *testing.T, buf *bytes.Buffer) []toolCall {
	t.Helper()
	var out []toolCall
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var c toolCall
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatalf("log line is not JSON: %v (%s)", err, line)
		}
		if c.Msg == mcpToolCallMsg {
			out = append(out, c)
		}
	}
	return out
}

// Gate 2 of the launch plan counts MCP tool calls by distinct users other than
// the owner. Prometheus cannot carry a user label without unbounded
// cardinality, so the user lands in a structured log line and the counter
// carries the volume.
func TestGuardToolLogsTheCallerAndTool(t *testing.T) {
	var buf bytes.Buffer
	deps := Deps{Logger: slog.New(slog.NewJSONHandler(&buf, nil))}

	handler := guardTool(deps, "search_pois",
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, string, error) {
			return nil, "ok", nil
		})

	if _, _, err := handler(callerContext("user-42", apikey.ScopeRead), nil, struct{}{}); err != nil {
		t.Fatalf("handler: %v", err)
	}

	calls := decodeCalls(t, &buf)
	if len(calls) != 1 {
		t.Fatalf("logged %d tool calls, want 1", len(calls))
	}
	if calls[0].Tool != "search_pois" || calls[0].UserID != "user-42" || calls[0].Outcome != outcomeOK {
		t.Fatalf("logged %+v, want search_pois/user-42/%s", calls[0], outcomeOK)
	}
}

func TestGuardToolRecordsAScopeDenialSeparately(t *testing.T) {
	var buf bytes.Buffer
	deps := Deps{Logger: slog.New(slog.NewJSONHandler(&buf, nil))}
	reached := false

	handler := guardTool(deps, "add_favorite",
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, string, error) {
			reached = true
			return nil, "", nil
		})

	// A read-only key may not call a mutating tool.
	if _, _, err := handler(callerContext("user-9", apikey.ScopeRead), nil, struct{}{}); err == nil {
		t.Fatal("expected the scope check to refuse a read-only key")
	}
	if reached {
		t.Fatal("handler ran despite the scope denial")
	}

	calls := decodeCalls(t, &buf)
	if len(calls) != 1 || calls[0].Outcome != outcomeDenied {
		t.Fatalf("logged %+v, want one call with outcome %s", calls, outcomeDenied)
	}
}

func TestGuardToolRecordsAToolError(t *testing.T) {
	var buf bytes.Buffer
	deps := Deps{Logger: slog.New(slog.NewJSONHandler(&buf, nil))}

	handler := guardTool(deps, "get_itinerary",
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, string, error) {
			return nil, "", errors.New("boom")
		})

	if _, _, err := handler(callerContext("user-1", apikey.ScopeRead), nil, struct{}{}); err == nil {
		t.Fatal("expected the tool error to surface")
	}

	calls := decodeCalls(t, &buf)
	if len(calls) != 1 || calls[0].Outcome != outcomeError {
		t.Fatalf("logged %+v, want one call with outcome %s", calls, outcomeError)
	}
}

// A nil logger must not panic: Deps.Logger is optional in tests and tools.
func TestGuardToolSurvivesANilLogger(t *testing.T) {
	handler := guardTool(Deps{}, "status",
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, string, error) {
			return nil, "ok", nil
		})
	if _, _, err := handler(callerContext("user-1", apikey.ScopeRead), nil, struct{}{}); err != nil {
		t.Fatalf("handler: %v", err)
	}
}
