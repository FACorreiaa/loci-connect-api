package integrations

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A session is exercised over the SDK's in-memory transport rather than an
// httptest server, because ParseGatewayURL refuses loopback — which is the
// point of it. That guard is tested directly in pkg/ai/providers; what needs
// covering here is how this package reads what a server sends back.
func testSession(t *testing.T) *Session {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{Name: "upstream", Version: "1"}, nil)

	type emptyInput struct{}
	mcp.AddTool(server, &mcp.Tool{Name: "list_events", Description: "list events"},
		func(context.Context, *mcp.CallToolRequest, emptyInput) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.TextContent{Text: "Tuesday: dentist"},
				&mcp.TextContent{Text: "Friday: flight"},
			}}, nil, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "explodes", Description: "always fails"},
		func(context.Context, *mcp.CallToolRequest, emptyInput) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "calendar is not shared"}},
			}, nil, nil
		})

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "loci", Version: "1"}, nil).
		Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	session := &Session{cs: cs}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestToolNamesReportsWhatTheServerOffers(t *testing.T) {
	names, err := testSession(t).ToolNames(t.Context())
	if err != nil {
		t.Fatalf("tool names: %v", err)
	}

	got := strings.Join(names, ",")
	if !strings.Contains(got, "list_events") {
		t.Errorf("tools = %v, want list_events among them", names)
	}
}

// Several text blocks are one answer; dropping all but the first would lose
// half of somebody's week.
func TestCallJoinsEveryTextBlock(t *testing.T) {
	out, err := testSession(t).Call(t.Context(), "list_events", nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !strings.Contains(out, "dentist") || !strings.Contains(out, "flight") {
		t.Errorf("output = %q, want both events", out)
	}
}

// A tool result flagged as an error is a failure even though the call itself
// succeeded. Returning its text as an answer would put "calendar is not shared"
// into an itinerary as though it were an event.
func TestAToolThatReportsAnErrorIsAFailure(t *testing.T) {
	out, err := testSession(t).Call(t.Context(), "explodes", nil)
	if err == nil {
		t.Fatalf("call returned %q and no error", out)
	}
	if !strings.Contains(err.Error(), "calendar is not shared") {
		t.Errorf("error = %q, want the server's own explanation", err)
	}
}

func TestCallingAToolThatDoesNotExistFails(t *testing.T) {
	if _, err := testSession(t).Call(t.Context(), "no_such_tool", nil); err == nil {
		t.Fatal("calling an unknown tool succeeded")
	}
}

// The endpoint is checked again at dial time, not only when it was stored.
func TestConnectRefusesAddressesLociWillNotDial(t *testing.T) {
	client := NewClient()

	for name, endpoint := range map[string]string{
		"loopback":  "http://127.0.0.1:9000/mcp",
		"localhost": "http://localhost:9000/mcp",
		"metadata":  "http://169.254.169.254/mcp",
		"empty":     "",
		"not a url": "hermes-box",
		"scheme":    "file:///etc/passwd",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := client.Connect(context.Background(), endpoint, "token"); err == nil {
				t.Fatal("the address was dialled")
			}
		})
	}
}

// Connect failures are shown to the owner and written to logs.
func TestConnectErrorsNeverNameTheToken(t *testing.T) {
	const token = "must-never-appear"

	_, err := NewClient().Connect(context.Background(), "http://127.0.0.1:9000/mcp", token)
	if err == nil {
		t.Fatal("no error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("the error names the token: %q", err)
	}
}
