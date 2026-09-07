package integrations

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/FACorreiaa/loci-connect-api/pkg/ai/providers"
)

// dialTimeout bounds a single call to somebody else's server.
//
// Short on purpose. These calls happen while an itinerary is being planned, and
// an upstream that has stopped answering must cost the request a few seconds,
// not the whole generation.
const dialTimeout = 10 * time.Second

// Client dials external MCP servers.
//
// One per process: the underlying transport is reused across users, so a
// connection to somebody's gateway does not cost a TLS handshake per call.
type Client struct {
	impl *mcp.Implementation
}

func NewClient() *Client {
	return &Client{impl: &mcp.Implementation{Name: "loci", Version: "1"}}
}

// Session is one open connection to an external server.
type Session struct {
	cs *mcp.ClientSession
}

// Connect opens a session against endpoint, presenting token if there is one.
//
// The endpoint is re-validated here even though it passed the same check when
// it was stored: this is the last point before the address is dialled, and a
// name that resolved to a public host then can be rebound onto loopback or
// cloud metadata now. The HTTP client re-checks each resolved address as well.
func (c *Client) Connect(ctx context.Context, endpoint, token string) (*Session, error) {
	safeEndpoint, err := providers.ParseGatewayURL(endpoint)
	if err != nil {
		return nil, fmt.Errorf("integrations: %w", err)
	}

	httpClient := providers.GatewayHTTPClient(&http.Client{Timeout: dialTimeout})
	if token != "" {
		httpClient = &http.Client{
			Timeout:   httpClient.Timeout,
			Transport: bearer{token: token, inner: httpClient.Transport},
			// Redirects are not followed for the same reason the dialer
			// re-checks: a redirect is an address we did not validate.
			CheckRedirect: httpClient.CheckRedirect,
		}
	}

	transport := &mcp.StreamableClientTransport{
		Endpoint:   safeEndpoint,
		HTTPClient: httpClient,
		// Request and response only. Loci calls these servers while answering
		// something; it has no use for server-initiated messages, and a
		// standalone SSE stream would be a connection held open per user for
		// notifications nothing reads.
		DisableStandaloneSSE: true,
	}

	cs, err := mcp.NewClient(c.impl, nil).Connect(ctx, transport, nil)
	if err != nil {
		// The error can carry request detail; the token travelled in a header.
		return nil, fmt.Errorf("integrations: could not reach %s", safeEndpoint)
	}
	return &Session{cs: cs}, nil
}

func (s *Session) Close() error { return s.cs.Close() }

// ToolNames lists what the server offers, so somebody can see it is the one
// they meant before anything depends on it.
func (s *Session) ToolNames(ctx context.Context) ([]string, error) {
	result, err := s.cs.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("integrations: could not list tools: %w", err)
	}

	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	return names, nil
}

// Call invokes one tool and returns its text content.
//
// Text only: what Loci does with the answer is put it in front of a model, and
// the other content types have no representation there.
func (s *Session) Call(ctx context.Context, name string, args map[string]any) (string, error) {
	result, err := s.cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "", fmt.Errorf("integrations: %s failed: %w", name, err)
	}

	var sb strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(text.Text)
		}
	}

	if result.IsError {
		// The server said the call failed. Its text is the explanation, and it
		// is more useful to the owner than anything this layer could add.
		return "", fmt.Errorf("integrations: %s reported an error: %s", name, sb.String())
	}
	return sb.String(), nil
}

// bearer attaches the stored token to every request to this server.
type bearer struct {
	token string
	inner http.RoundTripper
}

func (b bearer) RoundTrip(req *http.Request) (*http.Response, error) {
	// Cloned rather than mutated: a RoundTripper does not own the request it
	// is given, and the SDK may retry with it.
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+b.token)

	inner := b.inner
	if inner == nil {
		inner = http.DefaultTransport
	}
	return inner.RoundTrip(clone)
}
