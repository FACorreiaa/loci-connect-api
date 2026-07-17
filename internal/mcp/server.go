// Package mcp exposes Loci's POI, itinerary, and list capabilities as
// Model Context Protocol tools over Streamable HTTP. Clients (Claude,
// Codex, Gemini CLI, ...) authenticate with a loci_sk_ API key; tools run
// with the key owner's identity against the existing service layer.
package mcp

import (
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/apikey"
	chatservice "github.com/FACorreiaa/loci-connect-api/internal/domain/chat/service"
	itinerarylist "github.com/FACorreiaa/loci-connect-api/internal/domain/list"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/poi"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
)

// Path is where the Streamable HTTP MCP endpoint is mounted on the API mux.
const Path = "/mcp"

const serverName = "loci"

// Version is stamped into the MCP handshake; keep in step with releases.
var Version = "dev"

// Deps are the service-layer dependencies MCP tools call into.
type Deps struct {
	POIService    poi.Service
	ListService   itinerarylist.Service
	ChatService   chatservice.LlmInteractiontService
	APIKeyService apikey.Service
	Subscription  subscription.Service
	Logger        *slog.Logger
}

// Handler returns the authenticated Streamable HTTP handler to mount at Path.
func Handler(deps Deps) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Version: Version,
	}, nil)
	registerPOITools(server, deps)
	registerItineraryTools(server, deps)
	registerListTools(server, deps)
	if deps.ChatService != nil && deps.Subscription != nil {
		registerChatTools(server, deps)
	}

	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: true})

	return authMiddleware(deps, streamable)
}
