package recommendation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	recommendationv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recommendation"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recommendation/recommendationconnect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/travelhistory"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

const tasteAlgorithmVersion = "taste-v1"

// Handler owns recommendation attribution, learning controls, and taste transparency.
type Handler struct {
	recommendationconnect.UnimplementedRecommendationServiceHandler
	db     *pgxpool.Pool
	logger *slog.Logger

	// history records confirmed visits as travel history. Optional and
	// best-effort: never nil after NewHandler, and never able to fail an event
	// submission — see WithTravelHistory.
	history travelhistory.Recorder
}

// WithTravelHistory attaches the travel-history recorder so a confirmed visit
// also becomes a row in the traveller's history.
//
// Optional by design: a nil recorder degrades to recording no history rather
// than to failing the event, which is the same policy the preference recorder
// uses.
func (h *Handler) WithTravelHistory(r travelhistory.Recorder) *Handler {
	if r == nil {
		r = travelhistory.NopRecorder{}
	}
	h.history = r
	return h
}

func NewHandler(db *pgxpool.Pool, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		db:      db,
		logger:  logger.With(slog.String("component", "recommendation")),
		history: travelhistory.NopRecorder{},
	}
}

// IssueTraces records the exact attribution values the server returned to a
// user. RecordEvents fails closed unless its trace matches one of these rows.
func (h *Handler) IssueTraces(ctx context.Context, userID uuid.UUID, traces []*recommendationv1.RecommendationTrace) error {
	if h.db == nil {
		return errors.New("recommendation store unavailable")
	}
	if userID == uuid.Nil {
		return errors.New("user ID is required")
	}
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin issuing recommendation traces: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	for _, trace := range traces {
		if err := validateTrace(trace); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO issued_recommendations (
				user_id, run_id, item_id, rank, algorithm_version,
				experiment_variant, surface, channel
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (user_id, run_id, item_id, rank) DO UPDATE SET
				algorithm_version = EXCLUDED.algorithm_version,
				experiment_variant = EXCLUDED.experiment_variant,
				surface = EXCLUDED.surface,
				channel = EXCLUDED.channel`,
			userID, trace.GetRunId(), trace.GetItemId(), trace.GetRank(),
			trace.GetAlgorithmVersion(), trace.GetExperimentVariant(),
			enumValue(trace.GetSurface().String(), "RECOMMENDATION_SURFACE_"),
			enumValue(trace.GetChannel().String(), "RECOMMENDATION_CHANNEL_"))
		if err != nil {
			return fmt.Errorf("issue recommendation trace: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit recommendation traces: %w", err)
	}
	return nil
}

func validateTrace(trace *recommendationv1.RecommendationTrace) error {
	if trace == nil {
		return errors.New("recommendation trace is required")
	}
	if trace.GetRunId() == "" || trace.GetItemId() == "" || trace.GetAlgorithmVersion() == "" || trace.GetExperimentVariant() == "" {
		return errors.New("recommendation trace is incomplete")
	}
	if len(trace.GetRunId()) > 128 || len(trace.GetItemId()) > 256 || len(trace.GetAlgorithmVersion()) > 128 || len(trace.GetExperimentVariant()) > 64 {
		return errors.New("recommendation trace is too large")
	}
	if trace.GetRank() < 0 ||
		trace.GetSurface() == recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_UNSPECIFIED ||
		trace.GetChannel() == recommendationv1.RecommendationChannel_RECOMMENDATION_CHANNEL_UNSPECIFIED {
		return errors.New("recommendation trace is invalid")
	}
	return nil
}

func eventFingerprint(userID uuid.UUID, event *recommendationv1.RecommendationEvent, metadata []byte, tripID *uuid.UUID) [sha256.Size]byte {
	trace := event.GetTrace()
	rating := ""
	if event.Rating != nil {
		rating = fmt.Sprint(event.GetRating())
	}
	trip := ""
	if tripID != nil {
		trip = tripID.String()
	}
	payload := strings.Join([]string{
		userID.String(), enumValue(event.GetEventType().String(), "RECOMMENDATION_EVENT_TYPE_"), trace.GetRunId(), trace.GetItemId(),
		event.GetPoiId(), trip, rating, string(metadata),
	}, "\x1f")
	return sha256.Sum256([]byte(payload))
}

func traceWasIssued(ctx context.Context, tx pgx.Tx, userID uuid.UUID, trace *recommendationv1.RecommendationTrace) (bool, error) {
	var issued bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM issued_recommendations
			WHERE user_id = $1 AND run_id = $2 AND item_id = $3 AND rank = $4
			  AND algorithm_version = $5 AND experiment_variant = $6
			  AND surface = $7 AND channel = $8
		)`, userID, trace.GetRunId(), trace.GetItemId(), trace.GetRank(),
		trace.GetAlgorithmVersion(), trace.GetExperimentVariant(),
		enumValue(trace.GetSurface().String(), "RECOMMENDATION_SURFACE_"),
		enumValue(trace.GetChannel().String(), "RECOMMENDATION_CHANNEL_")).Scan(&issued)
	return issued, err
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
	// Visits derived from accepted events, written to travel history after the
	// commit so they can never roll the events back.
	var visited []visitCandidate
	if len(req.Msg.GetEvents()) > 100 {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("too many recommendation events"))
	}
	for _, event := range req.Msg.GetEvents() {
		trace := event.GetTrace()
		if _, parseErr := uuid.Parse(event.GetClientEventId()); parseErr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid client event ID"))
		}
		if traceErr := validateTrace(trace); traceErr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, traceErr)
		}
		if event.GetEventType() == recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_UNSPECIFIED {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("event type is required"))
		}
		if event.GetEventType() == recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_RATED &&
			(event.Rating == nil || event.GetRating() < 1 || event.GetRating() > 5) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("rating must be between 1 and 5"))
		}
		if event.GetOccurredAt() == nil || event.GetOccurredAt().CheckValid() != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("valid occurred_at is required"))
		}
		if event.GetOccurredAt().AsTime().After(time.Now().Add(5 * time.Minute)) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("occurred_at is in the future"))
		}
		if event.GetPoiId() != "" && event.GetPoiId() != trace.GetItemId() {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("POI does not match recommendation item"))
		}
		issued, issuedErr := traceWasIssued(ctx, tx, userID, trace)
		if issuedErr != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("validate recommendation trace: %w", issuedErr))
		}
		if !issued {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("recommendation trace was not issued to this user"))
		}
		metadata, marshalErr := json.Marshal(event.GetMetadata())
		if marshalErr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("encode event metadata: %w", marshalErr))
		}
		if len(metadata) > 16*1024 {
			return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("event metadata is too large"))
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
		fingerprint := eventFingerprint(userID, event, metadata, tripID)
		result, execErr := tx.Exec(ctx, `
			INSERT INTO recommendation_events (
				client_event_id, user_id, event_type, run_id, item_id, poi_id, trip_id,
				rank, algorithm_version, experiment_variant, surface, channel, rating,
				metadata, contribute_aggregate, occurred_at, event_fingerprint
			) VALUES (
				$1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9, $10, $11, $12, $13, $14,
				COALESCE((SELECT contribute_aggregate FROM personalization_settings WHERE user_id = $2), FALSE),
				$15, $16
			)
			ON CONFLICT DO NOTHING`,
			event.GetClientEventId(), userID,
			enumValue(event.GetEventType().String(), "RECOMMENDATION_EVENT_TYPE_"),
			trace.GetRunId(), trace.GetItemId(), event.GetPoiId(), tripID, trace.GetRank(),
			trace.GetAlgorithmVersion(), trace.GetExperimentVariant(),
			enumValue(trace.GetSurface().String(), "RECOMMENDATION_SURFACE_"),
			enumValue(trace.GetChannel().String(), "RECOMMENDATION_CHANNEL_"),
			event.Rating, metadata, occurredAt, fingerprint[:])
		if execErr != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("record recommendation event: %w", execErr))
		}
		if result.RowsAffected() == 0 {
			duplicates++
			continue
		}
		accepted++

		if visitConfirming(event.GetEventType()) && event.GetPoiId() != "" {
			// Collected here, written after the commit: a travel-history row is
			// derived from this event, so it must never be able to roll the
			// event itself back.
			visited = append(visited, visitCandidate{
				poiID:      event.GetPoiId(),
				tripID:     tripID,
				occurredAt: occurredAt,
			})
		}

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

	h.recordTravelHistory(ctx, userID, visited)

	return connect.NewResponse(&recommendationv1.RecordEventsResponse{Accepted: accepted, Duplicates: duplicates}), nil
}

// visitCandidate is an accepted event that is evidence of an actual visit.
type visitCandidate struct {
	poiID      string
	tripID     *uuid.UUID
	occurredAt time.Time
}

// visitConfirming reports whether an event means the traveller was physically
// there.
//
// RATED counts alongside VISIT_CONFIRMED because learningEvent() already treats
// the two identically (both produce the "visited" preference signal). Splitting
// them here would make travel history disagree with the rest of the system about
// what counts as having been somewhere.
func visitConfirming(t recommendationv1.RecommendationEventType) bool {
	switch t {
	case recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_VISIT_CONFIRMED,
		recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_RATED:
		return true
	default:
		return false
	}
}

// recordTravelHistory resolves each confirmed stop to a placed city and hands it
// to the recorder.
//
// Runs after the commit and swallows its own errors. A stop we cannot place is
// skipped rather than recorded at a guessed location: an absent city is honest,
// an invented one is the class of bug this domain exists to remove.
func (h *Handler) recordTravelHistory(ctx context.Context, userID uuid.UUID, candidates []visitCandidate) {
	if h.history == nil || h.db == nil || len(candidates) == 0 {
		return
	}
	for _, c := range candidates {
		var (
			poiName           string
			lat, lon          *float64
			cityID            *uuid.UUID
			cityName, country *string
		)
		err := h.db.QueryRow(ctx, `
			SELECT p.name, ST_Y(p.location), ST_X(p.location), p.city_id, c.name, c.country
			FROM points_of_interest p
			LEFT JOIN cities c ON c.id = p.city_id
			WHERE p.id::text = $1`, c.poiID,
		).Scan(&poiName, &lat, &lon, &cityID, &cityName, &country)
		if err != nil {
			h.logger.Debug("skip travel history: poi not resolvable",
				slog.String("poi_id", c.poiID), slog.String("error", err.Error()))
			continue
		}
		if lat == nil || lon == nil || cityName == nil || *cityName == "" {
			continue
		}

		in := travelhistory.VisitInput{
			CityID:    cityID,
			CityName:  *cityName,
			Latitude:  *lat,
			Longitude: *lon,
			Source:    travelhistory.SourceVisitEvent,
			TripID:    c.tripID,
			VisitedAt: c.occurredAt,
			POIID:     c.poiID,
			POIName:   poiName,
		}
		if country != nil {
			in.Country = *country
		}
		h.history.RecordVisit(ctx, userID, in)
	}
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
