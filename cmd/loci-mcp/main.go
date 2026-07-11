// Command loci-mcp is a thin stdio proxy for Loci's hosted MCP endpoint.
// It lets MCP clients that only speak stdio (or users who prefer a local
// binary) use the remote server without duplicating any tool logic.
//
// Usage:
//
//	LOCI_API_KEY=loci_sk_... loci-mcp [-endpoint https://api.example.com/mcp]
//
// Every tool advertised by the remote server is mirrored locally and
// forwarded verbatim.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultEndpoint = "http://localhost:8080/mcp"

// version is stamped by GoReleaser via -ldflags.
var version = "dev"

func main() {
	endpoint := flag.String("endpoint", envOr("LOCI_MCP_ENDPOINT", defaultEndpoint), "remote Loci MCP endpoint URL")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	apiKey := os.Getenv("LOCI_API_KEY")
	if apiKey == "" {
		log.Fatal("LOCI_API_KEY is required (create one in Loci settings)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, *endpoint, apiKey); err != nil {
		log.Fatal(err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// authTransport injects the API key into every request to the remote server.
type authTransport struct {
	apiKey string
	base   http.RoundTripper
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.apiKey)
	return t.base.RoundTrip(clone)
}

func run(ctx context.Context, endpoint, apiKey string) error {
	client := mcp.NewClient(&mcp.Implementation{Name: "loci-mcp-proxy", Version: version}, nil)
	remote, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Transport: &authTransport{apiKey: apiKey, base: http.DefaultTransport},
		},
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to %s (check LOCI_API_KEY and endpoint): %w", endpoint, err)
	}
	defer remote.Close()

	tools, err := remote.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to list remote tools: %w", err)
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "loci", Version: version}, nil)
	for _, tool := range tools.Tools {
		server.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return remote.CallTool(ctx, &mcp.CallToolParams{
				Name:      req.Params.Name,
				Arguments: req.Params.Arguments,
			})
		})
	}

	return server.Run(ctx, &mcp.StdioTransport{})
}
