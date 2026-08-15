package placeintel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	placev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/place"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/place/placeconnect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

const claimsRequiredForVerification = 2

type Handler struct {
	placeconnect.UnimplementedPlaceIntelligenceServiceHandler
	db     *pgxpool.Pool
	logger *slog.Logger
}

func NewHandler(db *pgxpool.Pool, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{db: db, logger: logger.With(slog.String("component", "place-intelligence"))}
}

func userID(ctx context.Context) (uuid.UUID, error) {
	raw, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || raw == "" {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}
	return id, nil
}

func fieldName(field placev1.PlaceFactField) string {
	return strings.ToLower(strings.TrimPrefix(field.String(), "PLACE_FACT_FIELD_"))
}

func parseField(value string) placev1.PlaceFactField {
	key := "PLACE_FACT_FIELD_" + strings.ToUpper(value)
	if enumValue, ok := placev1.PlaceFactField_value[key]; ok {
		return placev1.PlaceFactField(enumValue)
	}
	return placev1.PlaceFactField_PLACE_FACT_FIELD_UNSPECIFIED
}

func parseStatus(value string) placev1.PlaceClaimStatus {
	key := "PLACE_CLAIM_STATUS_" + strings.ToUpper(value)
	if enumValue, ok := placev1.PlaceClaimStatus_value[key]; ok {
		return placev1.PlaceClaimStatus(enumValue)
	}
	return placev1.PlaceClaimStatus_PLACE_CLAIM_STATUS_UNSPECIFIED
}

func factLifetime(field placev1.PlaceFactField) time.Duration {
	switch field {
	case placev1.PlaceFactField_PLACE_FACT_FIELD_OPENING_HOURS,
		placev1.PlaceFactField_PLACE_FACT_FIELD_CROWD_LEVEL,
		placev1.PlaceFactField_PLACE_FACT_FIELD_NOISE_LEVEL:
		return 30 * 24 * time.Hour
	default:
		return 180 * 24 * time.Hour
	}
}

func (h *Handler) GetPlaceFacts(ctx context.Context, req *connect.Request[placev1.GetPlaceFactsRequest]) (*connect.Response[placev1.PlaceFacts], error) {
	if _, err := userID(ctx); err != nil {
		return nil, err
	}
	rows, err := h.db.Query(ctx, `
		SELECT field, value, confidence, contributor_count, verified_at, expires_at
		FROM place_facts
		WHERE poi_id = $1 AND expires_at > NOW()
		ORDER BY field`, req.Msg.GetPoiId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get place facts: %w", err))
	}
	defer rows.Close()
	facts := make([]*placev1.PlaceFact, 0)
	for rows.Next() {
		var (
			field                 string
			verifiedAt, expiresAt time.Time
		)
		fact := &placev1.PlaceFact{}
		if err := rows.Scan(&field, &fact.Value, &fact.Confidence, &fact.ContributorCount, &verifiedAt, &expiresAt); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scan place fact: %w", err))
		}
		fact.Field = parseField(field)
		fact.VerifiedAt = timestamppb.New(verifiedAt)
		fact.ExpiresAt = timestamppb.New(expiresAt)
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("iterate place facts: %w", err))
	}
	return connect.NewResponse(&placev1.PlaceFacts{PoiId: req.Msg.GetPoiId(), Facts: facts}), nil
}

func (h *Handler) ListVerificationTasks(ctx context.Context, req *connect.Request[placev1.ListVerificationTasksRequest]) (*connect.Response[placev1.ListVerificationTasksResponse], error) {
	if _, err := userID(ctx); err != nil {
		return nil, err
	}
	limit := req.Msg.GetLimit()
	if limit <= 0 {
		limit = 10
	}
	rows, err := h.db.Query(ctx, `
		SELECT p.id::text, p.name, MIN(f.verified_at)
		FROM points_of_interest p
		LEFT JOIN place_facts f ON f.poi_id = p.id::text AND f.expires_at > NOW()
		GROUP BY p.id, p.name
		HAVING COUNT(f.field) < 3
		ORDER BY MIN(f.verified_at) NULLS FIRST, p.updated_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list verification tasks: %w", err))
	}
	defer rows.Close()
	tasks := make([]*placev1.VerificationTask, 0, limit)
	for rows.Next() {
		var oldest *time.Time
		task := &placev1.VerificationTask{
			RequestedFields: []placev1.PlaceFactField{
				placev1.PlaceFactField_PLACE_FACT_FIELD_OPENING_HOURS,
				placev1.PlaceFactField_PLACE_FACT_FIELD_ACCESSIBILITY,
				placev1.PlaceFactField_PLACE_FACT_FIELD_VIBE,
			},
		}
		if err := rows.Scan(&task.PoiId, &task.PoiName, &oldest); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scan verification task: %w", err))
		}
		if oldest != nil {
			task.OldestFactAt = timestamppb.New(*oldest)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("iterate verification tasks: %w", err))
	}
	return connect.NewResponse(&placev1.ListVerificationTasksResponse{Tasks: tasks}), nil
}

func (h *Handler) SubmitPlaceClaim(ctx context.Context, req *connect.Request[placev1.SubmitPlaceClaimRequest]) (*connect.Response[placev1.SubmitPlaceClaimResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("begin place claim: %w", err))
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var claimID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO place_claims (client_claim_id, poi_id, user_id, field, value, observed_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (client_claim_id) DO NOTHING
		RETURNING id`, req.Msg.GetClientClaimId(), req.Msg.GetPoiId(), uid,
		fieldName(req.Msg.GetField()), strings.TrimSpace(req.Msg.GetValue()), req.Msg.GetObservedAt().AsTime()).Scan(&claimID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingStatus string
		if queryErr := tx.QueryRow(ctx, `
			SELECT id, status FROM place_claims WHERE client_claim_id = $1 AND user_id = $2`,
			req.Msg.GetClientClaimId(), uid).Scan(&claimID, &existingStatus); queryErr != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get duplicate place claim: %w", queryErr))
		}
		return connect.NewResponse(&placev1.SubmitPlaceClaimResponse{
			ClaimId: claimID.String(), Status: parseStatus(existingStatus),
		}), nil
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("insert place claim: %w", err))
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO contributor_profiles (user_id, submitted_claims)
		VALUES ($1, 1)
		ON CONFLICT (user_id) DO UPDATE SET
			submitted_claims = contributor_profiles.submitted_claims + 1,
			updated_at = NOW()`, uid); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("update contributor profile: %w", err))
	}

	var contributorCount int32
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(DISTINCT user_id)::integer
		FROM place_claims
		WHERE poi_id = $1 AND field = $2 AND LOWER(value) = LOWER($3)
		  AND observed_at > NOW() - INTERVAL '180 days'`, req.Msg.GetPoiId(), fieldName(req.Msg.GetField()), strings.TrimSpace(req.Msg.GetValue())).
		Scan(&contributorCount); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("count corroborating claims: %w", err))
	}
	status := placev1.PlaceClaimStatus_PLACE_CLAIM_STATUS_PENDING
	if contributorCount >= claimsRequiredForVerification {
		expiresAt := time.Now().Add(factLifetime(req.Msg.GetField()))
		confidence := 0.6 + float64(contributorCount-claimsRequiredForVerification)*0.1
		if confidence > 0.95 {
			confidence = 0.95
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO place_facts (poi_id, field, value, confidence, contributor_count, verified_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, NOW(), $6)
			ON CONFLICT (poi_id, field) DO UPDATE SET
				value = EXCLUDED.value,
				confidence = EXCLUDED.confidence,
				contributor_count = EXCLUDED.contributor_count,
				verified_at = NOW(),
				expires_at = EXCLUDED.expires_at,
				updated_at = NOW()`, req.Msg.GetPoiId(), fieldName(req.Msg.GetField()), strings.TrimSpace(req.Msg.GetValue()), confidence, contributorCount, expiresAt); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("upsert place fact: %w", err))
		}
		if _, err := tx.Exec(ctx, `
			UPDATE place_claims SET status = 'accepted'
			WHERE poi_id = $1 AND field = $2 AND LOWER(value) = LOWER($3) AND status = 'pending'`,
			req.Msg.GetPoiId(), fieldName(req.Msg.GetField()), strings.TrimSpace(req.Msg.GetValue())); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("accept corroborated claims: %w", err))
		}
		if _, err := tx.Exec(ctx, `
			UPDATE contributor_profiles SET
				accepted_claims = accepted_claims + 1,
				reputation = LEAST(100, reputation + 3),
				badges = CASE WHEN accepted_claims + 1 >= 10 AND NOT ('local-scout' = ANY(badges))
					THEN array_append(badges, 'local-scout') ELSE badges END,
				updated_at = NOW()
			WHERE user_id = $1`, uid); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("reward contributor: %w", err))
		}
		status = placev1.PlaceClaimStatus_PLACE_CLAIM_STATUS_ACCEPTED
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("commit place claim: %w", err))
	}
	h.logger.InfoContext(ctx, "place claim submitted", slog.String("claim_id", claimID.String()), slog.String("status", status.String()))
	return connect.NewResponse(&placev1.SubmitPlaceClaimResponse{ClaimId: claimID.String(), Status: status}), nil
}

func (h *Handler) GetMyContributorProfile(ctx context.Context, _ *connect.Request[placev1.GetMyContributorProfileRequest]) (*connect.Response[placev1.ContributorProfile], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	profile := &placev1.ContributorProfile{Badges: make([]string, 0)}
	err = h.db.QueryRow(ctx, `
		SELECT reputation, submitted_claims, accepted_claims, badges
		FROM contributor_profiles WHERE user_id = $1`, uid).
		Scan(&profile.Reputation, &profile.SubmittedClaims, &profile.AcceptedClaims, &profile.Badges)
	if errors.Is(err, pgx.ErrNoRows) {
		return connect.NewResponse(profile), nil
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get contributor profile: %w", err))
	}
	return connect.NewResponse(profile), nil
}
