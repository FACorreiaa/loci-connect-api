package recommendation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	recommendationv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/recommendation"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/recommendation/recommendationconnect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

const tasteAlgorithmVersion = "taste-v1"

// Handler owns recommendation attribution, learning controls, and taste transparency.
type Handler struct {
	recommendationconnect.UnimplementedRecommendationServiceHandler
	db     *pgxpool.Pool
	logger *slog.Logger
}

func NewHandler(db *pgxpool.Pool, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{db: db, logger: logger.With(slog.String("component", "recommendation"))}
}

func authenticatedUserID(ctx context.Context) (uuid.UUID, error) {
	raw, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || raw == "" {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}
	return userID, nil
}

func enumValue(value, prefix string) string {
	return strings.ToLower(strings.TrimPrefix(value, prefix))
}

func learningEvent(eventType recommendationv1.RecommendationEventType) (string, bool) {
	switch eventType {
	case recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_FAVORITED:
		return "favorited", true
	case recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_ADDED_TO_LIST,
		recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_ADDED_TO_TRIP:
		return "saved", true
	case recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_KEPT_IN_TRIP:
		return "reordered", true
	case recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_DISMISSED,
		recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_REMOVED_FROM_TRIP:
		return "skipped", true
	case recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_EXPORTED:
		return "exported", true
	case recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_VISIT_CONFIRMED,
		recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_RATED:
		return "visited", true
	default:
		return "", false
	}
}

func eventWeight(event *recommendationv1.RecommendationEvent) float32 {
	if event.GetEventType() == recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_RATED {
		return float32(event.GetRating()) / 5
	}
	if event.GetEventType() == recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_DISMISSED ||
		event.GetEventType() == recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_REMOVED_FROM_TRIP {
		return -0.5
	}
	return 1
}

func (h *Handler) RecordEvents(ctx context.Context, req *connect.Request[recommendationv1.RecordEventsRequest]) (*connect.Response[recommendationv1.RecordEventsResponse], error) {
	userID, err := authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}
	if h.db == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("recommendation store unavailable"))
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("begin recommendation events: %w", err))
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	accepted := int32(0)
	duplicates := int32(0)
	for _, event := range req.Msg.GetEvents() {
		trace := event.GetTrace()
		metadata, marshalErr := json.Marshal(event.GetMetadata())
		if marshalErr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("encode event metadata: %w", marshalErr))
		}
		var tripID *uuid.UUID
		if event.GetTripId() != "" {
			parsed, parseErr := uuid.Parse(event.GetTripId())
			if parseErr != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid trip ID"))
			}
			tripID = &parsed
		}
		occurredAt := event.GetOccurredAt().AsTime()
		result, execErr := tx.Exec(ctx, `
			INSERT INTO recommendation_events (
				client_event_id, user_id, event_type, run_id, item_id, poi_id, trip_id,
				rank, algorithm_version, experiment_variant, surface, channel, rating,
				metadata, contribute_aggregate, occurred_at
			) VALUES (
				$1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9, $10, $11, $12, $13, $14,
				COALESCE((SELECT contribute_aggregate FROM personalization_settings WHERE user_id = $2), FALSE),
				$15
			)
			ON CONFLICT (client_event_id) DO NOTHING`,
			event.GetClientEventId(), userID,
			enumValue(event.GetEventType().String(), "RECOMMENDATION_EVENT_TYPE_"),
			trace.GetRunId(), trace.GetItemId(), event.GetPoiId(), tripID, trace.GetRank(),
			trace.GetAlgorithmVersion(), trace.GetExperimentVariant(),
			enumValue(trace.GetSurface().String(), "RECOMMENDATION_SURFACE_"),
			enumValue(trace.GetChannel().String(), "RECOMMENDATION_CHANNEL_"),
			event.Rating, metadata, occurredAt)
		if execErr != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("record recommendation event: %w", execErr))
		}
		if result.RowsAffected() == 0 {
			duplicates++
			continue
		}
		accepted++

		preferenceEvent, learns := learningEvent(event.GetEventType())
		if !learns {
			continue
		}
		_, execErr = tx.Exec(ctx, `
			INSERT INTO preference_feedback (user_id, poi_id, trip_id, event, weight, metadata)
			SELECT $1, NULLIF($2, ''), $3, $4, $5, $6
			WHERE COALESCE((
				SELECT personalization_enabled FROM personalization_settings WHERE user_id = $1
			), TRUE)`, userID, event.GetPoiId(), tripID, preferenceEvent, eventWeight(event), metadata)
		if execErr != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("record learning signal: %w", execErr))
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("commit recommendation events: %w", err))
	}
	return connect.NewResponse(&recommendationv1.RecordEventsResponse{Accepted: accepted, Duplicates: duplicates}), nil
}

func (h *Handler) GetPersonalizationSettings(ctx context.Context, _ *connect.Request[recommendationv1.GetPersonalizationSettingsRequest]) (*connect.Response[recommendationv1.PersonalizationSettings], error) {
	userID, err := authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}
	settings, err := h.settings(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(settings), nil
}

func (h *Handler) settings(ctx context.Context, userID uuid.UUID) (*recommendationv1.PersonalizationSettings, error) {
	var (
		personalizationEnabled bool
		contributeAggregate    bool
		disclosureSeen         bool
		updatedAt              time.Time
	)
	err := h.db.QueryRow(ctx, `
		INSERT INTO personalization_settings (user_id)
		VALUES ($1)
		ON CONFLICT (user_id) DO UPDATE SET user_id = EXCLUDED.user_id
		RETURNING personalization_enabled, contribute_aggregate, disclosure_seen, updated_at`, userID).
		Scan(&personalizationEnabled, &contributeAggregate, &disclosureSeen, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("get personalization settings: %w", err)
	}
	return &recommendationv1.PersonalizationSettings{
		PersonalizationEnabled: personalizationEnabled,
		ContributeAggregate:    contributeAggregate,
		DisclosureSeen:         disclosureSeen,
		UpdatedAt:              timestamppb.New(updatedAt),
	}, nil
}

func (h *Handler) UpdatePersonalizationSettings(ctx context.Context, req *connect.Request[recommendationv1.UpdatePersonalizationSettingsRequest]) (*connect.Response[recommendationv1.PersonalizationSettings], error) {
	userID, err := authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}
	_, err = h.db.Exec(ctx, `
		INSERT INTO personalization_settings (
			user_id, personalization_enabled, contribute_aggregate, disclosure_seen, updated_at
		) VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			personalization_enabled = EXCLUDED.personalization_enabled,
			contribute_aggregate = EXCLUDED.contribute_aggregate,
			disclosure_seen = EXCLUDED.disclosure_seen,
			updated_at = NOW()`, userID, req.Msg.GetPersonalizationEnabled(), req.Msg.GetContributeAggregate(), req.Msg.GetDisclosureSeen())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("update personalization settings: %w", err))
	}
	settings, err := h.settings(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(settings), nil
}

func (h *Handler) GetTasteProfile(ctx context.Context, _ *connect.Request[recommendationv1.GetTasteProfileRequest]) (*connect.Response[recommendationv1.TasteProfile], error) {
	userID, err := authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := h.db.Query(ctx, `
		SELECT trait_key, label, score, confidence, evidence_count
		FROM user_taste_traits WHERE user_id = $1
		ORDER BY confidence DESC, evidence_count DESC, trait_key`, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list taste traits: %w", err))
	}
	defer rows.Close()
	traits := make([]*recommendationv1.TasteTrait, 0)
	for rows.Next() {
		trait := &recommendationv1.TasteTrait{}
		if err := rows.Scan(&trait.Key, &trait.Label, &trait.Score, &trait.Confidence, &trait.EvidenceCount); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scan taste trait: %w", err))
		}
		traits = append(traits, trait)
	}
	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("iterate taste traits: %w", err))
	}

	var feedbackCount int32
	var lastFeedbackAt *time.Time
	if err := h.db.QueryRow(ctx, `
		SELECT COUNT(*)::integer, MAX(created_at) FROM preference_feedback WHERE user_id = $1`, userID).
		Scan(&feedbackCount, &lastFeedbackAt); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("summarize taste feedback: %w", err))
	}
	now := timestamppb.Now()
	profile := &recommendationv1.TasteProfile{
		Traits:           traits,
		FeedbackCount:    feedbackCount,
		AlgorithmVersion: tasteAlgorithmVersion,
		UpdatedAt:        now,
	}
	if lastFeedbackAt != nil {
		profile.LastFeedbackAt = timestamppb.New(*lastFeedbackAt)
	}
	return connect.NewResponse(profile), nil
}

func (h *Handler) ResetTasteProfile(ctx context.Context, _ *connect.Request[recommendationv1.ResetTasteProfileRequest]) (*connect.Response[recommendationv1.ResetTasteProfileResponse], error) {
	userID, err := authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("begin taste reset: %w", err))
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	for _, statement := range []string{
		"DELETE FROM recommendation_events WHERE user_id = $1",
		"DELETE FROM preference_feedback WHERE user_id = $1",
		"DELETE FROM user_preference_vectors WHERE user_id = $1",
		"DELETE FROM user_taste_traits WHERE user_id = $1",
	} {
		if _, err := tx.Exec(ctx, statement, userID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("reset taste profile: %w", err))
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("commit taste reset: %w", err))
	}
	h.logger.InfoContext(ctx, "taste profile reset", slog.String("user_id", userID.String()))
	return connect.NewResponse(&recommendationv1.ResetTasteProfileResponse{Success: true}), nil
}
