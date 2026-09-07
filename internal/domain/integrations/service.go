package integrations

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/ai/providers"
	"github.com/FACorreiaa/loci-connect-api/pkg/secret"
)

// ErrSealingUnavailable means ENCRYPTION_KEY is not configured. A connection
// that needs a token is refused rather than stored with the token in the clear.
var ErrSealingUnavailable = errors.New("integrations: connecting a server is unavailable because no encryption key is configured")

// ErrUnknownProvider means the integration is not one Loci knows how to use.
var ErrUnknownProvider = errors.New("integrations: not an integration Loci supports")

// Provider is one integration a user may connect.
//
// A closed list, for the same reason the model-provider catalogue is one: these
// are addresses Loci will make requests to, and "connect anything" is a request
// forwarder rather than a feature.
type Provider struct {
	Name  string
	Label string
	Note  string
}

// Catalog is the integrations on offer.
var Catalog = []Provider{
	{
		Name: "hermes", Label: "Hermes",
		Note: "Your own Hermes instance, reachable over tailnet, LAN, or public HTTPS.",
	},
	{
		Name: "calendar", Label: "Calendar",
		Note: "An MCP server that can list your events, so a plan avoids the days you are busy.",
	},
}

// ProviderByName finds a catalogue entry.
func ProviderByName(name string) (Provider, bool) {
	for _, p := range Catalog {
		if p.Name == name {
			return p, true
		}
	}
	return Provider{}, false
}

// Service registers external MCP servers and opens sessions against them.
//
// A nil Sealer means no token can be stored. Servers that need no credential
// could still be connected, but allowing that would make "connected" mean two
// different things depending on configuration, so the whole feature is off.
type Service struct {
	repo   Repository
	client *Client
	sealer *secret.Sealer
}

func NewService(repo Repository, client *Client, sealer *secret.Sealer) *Service {
	return &Service{repo: repo, client: client, sealer: sealer}
}

// Enabled reports whether servers can be connected at all.
func (s *Service) Enabled() bool { return s != nil && s.sealer != nil }

// List returns the user's connections, without their tokens.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]Connection, error) {
	return s.repo.List(ctx, userID)
}

// Connect registers a server after checking that its address is one Loci will
// dial.
//
// An empty token keeps whatever is stored, which is what the form sends when
// somebody edits the endpoint and leaves the token field alone. When nothing is
// stored either, the server is registered without one — some need none.
func (s *Service) Connect(ctx context.Context, userID uuid.UUID, provider, endpoint, token string) (Connection, error) {
	if !s.Enabled() {
		return Connection{}, ErrSealingUnavailable
	}

	entry, ok := ProviderByName(strings.TrimSpace(provider))
	if !ok {
		return Connection{}, ErrUnknownProvider
	}

	safeEndpoint, err := providers.ParseGatewayURL(endpoint)
	if err != nil {
		// The endpoint is the user's own text and safe to report on.
		return Connection{}, fmt.Errorf("integrations: %w", err)
	}

	sealed, err := s.tokenToStore(ctx, userID, entry.Name, strings.TrimSpace(token))
	if err != nil {
		return Connection{}, err
	}
	return s.repo.Upsert(ctx, userID, entry.Name, safeEndpoint, sealed)
}

// tokenToStore seals a new token, or finds the stored one to keep.
func (s *Service) tokenToStore(ctx context.Context, userID uuid.UUID, provider, token string) (Sealed, error) {
	if token == "" {
		_, existing, err := s.repo.SealedToken(ctx, userID, provider)
		if errors.Is(err, ErrNotFound) {
			// First connection with no token: the server needs none.
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return existing, nil
	}

	const maxTokenLen = 4096
	if len(token) > maxTokenLen {
		return nil, errors.New("integrations: that does not look like an access token")
	}

	sealed, err := s.sealer.Seal(userID[:], []byte(token))
	if err != nil {
		// Deliberately not wrapped: the input is the token.
		return nil, errors.New("integrations: could not seal the access token")
	}
	return Sealed(sealed), nil
}

// Disconnect removes a connection.
func (s *Service) Disconnect(ctx context.Context, userID uuid.UUID, provider string) error {
	return s.repo.Delete(ctx, userID, provider)
}

// Open dials a connected server.
//
// Callers must Close the session. ErrNotFound means the user has not connected
// this integration, which is the ordinary case and a branch rather than a
// failure.
func (s *Service) Open(ctx context.Context, userID uuid.UUID, provider string) (*Session, error) {
	if !s.Enabled() {
		return nil, ErrNotFound
	}

	conn, sealed, err := s.repo.SealedToken(ctx, userID, provider)
	if err != nil {
		return nil, err
	}

	token := ""
	if len(sealed) > 0 {
		plaintext, err := s.sealer.Open(userID[:], sealed)
		if err != nil {
			// Rotated key, or a tampered row. Recorded so the settings page can
			// show the connection as failing rather than the itinerary quietly
			// arriving without whatever this server contributes.
			s.record(ctx, userID, provider, fmt.Errorf("the stored access token cannot be opened: %w", err))
			return nil, fmt.Errorf("integrations: the stored access token cannot be opened: %w", err)
		}
		token = string(plaintext)
	}

	session, err := s.client.Connect(ctx, conn.Endpoint, token)
	if err != nil {
		s.record(ctx, userID, provider, err)
		return nil, err
	}
	return session, nil
}

// Test dials the server and lists its tools, recording the outcome.
//
// It exists so somebody sees the connection working before anything depends on
// it, rather than discovering it was wrong through a plan that silently lacked
// their calendar.
func (s *Service) Test(ctx context.Context, userID uuid.UUID, provider string) ([]string, error) {
	session, err := s.Open(ctx, userID, provider)
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()

	names, err := session.ToolNames(ctx)
	if err != nil {
		s.record(ctx, userID, provider, err)
		return nil, err
	}

	if markErr := s.repo.MarkSeen(ctx, userID, provider); markErr != nil {
		// The call worked; failing the test because the note did not save
		// would report the opposite of what happened.
		return names, nil
	}
	return names, nil
}

// record notes why a call failed. Best effort: it runs on a path that has
// already failed, and a second failure must not replace the first.
func (s *Service) record(ctx context.Context, userID uuid.UUID, provider string, cause error) {
	_ = s.repo.RecordError(ctx, userID, provider, cause.Error())
}
