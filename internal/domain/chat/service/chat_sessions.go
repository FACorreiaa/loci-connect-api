package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	commonpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/common"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/concurrency"
)

// GetUserChatSessions retrieves paginated chat sessions for a user
func (l *ServiceImpl) GetUserChatSessions(ctx context.Context, userID uuid.UUID, page, limit int) (*locitypes.ChatSessionsResponse, error) {
	ctx, span := otel.Tracer("LlmInteractionService").Start(ctx, "GetUserChatSessions", trace.WithAttributes(
		attribute.String("user.id", userID.String()),
		attribute.Int("page", page),
		attribute.Int("limit", limit),
	))
	defer span.End()

	l.logger.InfoContext(ctx, "Retrieving paginated chat sessions for user",
		slog.String("userID", userID.String()),
		slog.Int("page", page),
		slog.Int("limit", limit))

	response, err := l.llmInteractionRepo.GetUserChatSessions(ctx, userID, page, limit)
	if err != nil {
		l.logger.ErrorContext(ctx, "Failed to get user chat sessions", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to get user chat sessions")
		return nil, fmt.Errorf("failed to get user chat sessions: %w", err)
	}

	l.logger.InfoContext(ctx, "Successfully retrieved paginated chat sessions",
		slog.String("userID", userID.String()),
		slog.Int("sessionCount", len(response.Sessions)),
		slog.Int("total", response.Total),
		slog.Int("page", response.Page),
		slog.Int("limit", response.Limit))
	span.SetAttributes(
		attribute.Int("sessions.count", len(response.Sessions)),
		attribute.Int("sessions.total", response.Total),
		attribute.Int("response.page", response.Page),
		attribute.Int("response.limit", response.Limit),
	)
	span.SetStatus(codes.Ok, "Chat sessions retrieved successfully")
	return response, nil
}

// GetChatSession returns a specific session if the user owns it.
func (l *ServiceImpl) GetChatSession(ctx context.Context, userID, sessionID uuid.UUID) (*locitypes.ChatSession, error) {
	session, err := l.llmInteractionRepo.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", common.ErrSessionNotFound, err)
	}
	if session.UserID != userID {
		return nil, common.ErrUnauthorized
	}
	return session, nil
}

// EndSession marks a chat session as closed.
func (l *ServiceImpl) EndSession(ctx context.Context, userID, sessionID uuid.UUID) error {
	session, err := l.llmInteractionRepo.GetSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("%w: %v", common.ErrSessionNotFound, err)
	}
	if session.UserID != userID {
		return common.ErrUnauthorized
	}
	session.Status = locitypes.StatusClosed
	session.UpdatedAt = time.Now()
	return l.llmInteractionRepo.UpdateSession(ctx, *session)
}

// GetRecentInteractions currently returns an empty response placeholder.
func (l *ServiceImpl) GetRecentInteractions(_ context.Context, _ uuid.UUID, _ *commonpb.PaginationRequest) (*chatv1.GetRecentInteractionsResponse, error) {
	// TODO: hook into repository once implemented.
	return &chatv1.GetRecentInteractionsResponse{}, nil
}

func (l *ServiceImpl) StartChat(ctx context.Context, userID, profileID uuid.UUID, cityName, message string, userLocation *locitypes.UserLocation) (*locitypes.ChatResponse, error) {
	eventCh := make(chan locitypes.StreamEvent, 100)
	cc := common.ChatContext{
		Ctx:          ctx,
		UserID:       userID,
		ProfileID:    profileID,
		CityName:     cityName,
		Message:      message,
		UserLocation: userLocation,
		EventCh:      eventCh,
	}
	// Written by the producer, read only after the range below. The channel
	// close orders the two. A caller must be told the turn failed rather than
	// handed an empty answer that reads as success: the Telegram bridge falls
	// back to a new session on an error, and silently could not.
	var streamErr error
	concurrency.Run(l.logger, func() {
		// Close when the producer returns, exactly as ContinueChat does.
		//
		// ProcessUnifiedChatMessageStream deliberately leaves the channel open
		// ("event channel will be closed by handler"): the streaming RPC path
		// closes it in the handler. StartChat has no handler above it, so
		// without this the range below waits on a channel nobody closes, and
		// any non-RPC caller — the Telegram bridge — hangs forever.
		defer close(eventCh)
		streamErr = l.ProcessUnifiedChatMessageStream(cc)
		if streamErr != nil {
			l.logger.Error("error processing stream", "error", streamErr)
		}
	})

	var lastItinerary locitypes.AiCityResponse
	var lastMessage string
	var sessionID uuid.UUID

	for event := range eventCh {
		if event.Type == locitypes.EventTypeItinerary {
			if itinerary, ok := event.Data.(locitypes.AiCityResponse); ok {
				lastItinerary = itinerary
				sessionID = itinerary.SessionID
			}
		}
		if event.Message != "" {
			lastMessage = event.Message
		}
	}

	if streamErr != nil {
		return nil, streamErr
	}

	return &locitypes.ChatResponse{
		SessionID:        sessionID,
		Message:          lastMessage,
		UpdatedItinerary: &lastItinerary,
	}, nil
}

// ContinueChat is a unary wrapper around the streaming continuation flow.

func (l *ServiceImpl) ContinueChat(ctx context.Context, _, sessionID uuid.UUID, message, _ string) (*locitypes.ChatResponse, error) {
	eventCh := make(chan locitypes.StreamEvent, 100) // Buffered channel to prevent blocking
	// See StartChat: the error belongs to the caller, not just the log.
	var streamErr error
	concurrency.Run(l.logger, func() {
		defer close(eventCh) // Ensure channel is closed when goroutine exits
		streamErr = l.ContinueSessionStreamed(ctx, sessionID, message, nil, eventCh)
		if streamErr != nil {
			l.logger.Error("error processing continue stream", "error", streamErr)
		}
	})

	var lastItinerary locitypes.AiCityResponse
	var lastMessage string

	for event := range eventCh {
		if event.Type == locitypes.EventTypeItinerary {
			// Try type assertion for pointer first
			if itinerary, ok := event.Data.(*locitypes.AiCityResponse); ok {
				lastItinerary = *itinerary
				l.logger.Debug("Captured itinerary from event (pointer)", "poi_count", len(itinerary.AIItineraryResponse.PointsOfInterest))
			} else if itinerary, ok := event.Data.(locitypes.AiCityResponse); ok {
				// Try value type
				lastItinerary = itinerary
				l.logger.Debug("Captured itinerary from event (value)", "poi_count", len(itinerary.AIItineraryResponse.PointsOfInterest))
			} else {
				l.logger.Warn("Failed to cast event data to AiCityResponse", "event_type", event.Type, "data_type", fmt.Sprintf("%T", event.Data))
			}
		}
		if event.Message != "" {
			lastMessage = event.Message
		}
	}

	if streamErr != nil {
		return nil, streamErr
	}

	return &locitypes.ChatResponse{
		SessionID:        sessionID,
		Message:          lastMessage,
		UpdatedItinerary: &lastItinerary,
	}, nil
}

// getPersonalizedPOI generates a prompt for personalized POIs

func (l *ServiceImpl) saveCityInteraction(ctx context.Context, interaction locitypes.LlmInteraction) (uuid.UUID, error) {
	ctx, span := otel.Tracer("LlmInteractionService").Start(ctx, "saveCityInteraction")
	defer span.End()

	if interaction.LatencyMs == 0 {
		// Ensure latency is set if not provided
		interaction.LatencyMs = int(time.Since(interaction.Timestamp).Milliseconds())
	}
	if interaction.ModelUsed == "" {
		interaction.ModelUsed = l.model // Default model
	}

	interactionID, err := l.llmInteractionRepo.SaveInteraction(ctx, interaction)
	if err != nil {
		span.RecordError(err)
		l.logger.WarnContext(ctx, "Failed to save LLM interaction", slog.Any("error", err))
		return uuid.Nil, fmt.Errorf("failed to save interaction: %w", err)
	}

	span.SetAttributes(attribute.String("interaction.id", interactionID.String()))
	return interactionID, nil
}

// handleSemanticAddPOIStreamed handles adding POIs with semantic search enhancement and streaming updates
