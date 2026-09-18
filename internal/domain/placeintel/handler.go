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

const (
	claimsRequiredForVerification = 2

	// recentInteractionDays bounds how far back a user's own browsing is taken
	// as a signal of where they actually are.
	recentInteractionDays = 90

	// maxRecentInteractions caps the interaction scan; a user with years of
	// history does not need all of it to place them in a handful of cities.
	maxRecentInteractions = 200

	// candidateMultiplier over-fetches POIs because a candidate whose fields are
	// all already covered is dropped, and we still want a full page afterwards.
	candidateMultiplier = 4
	maxCandidates       = 200

	// fallbackCityCount is how many cities a user with no history is shown
	// places from.
	fallbackCityCount = 5

	// pendingFactConfidence is what a single uncorroborated claim is worth. It
	// sits below the 0.6 a two-scout fact starts at, so ordering by confidence
	// keeps confirmed facts ahead of reported ones.
	pendingFactConfidence = 0.3
)

// cityResolver turns what a user typed into a city row, creating it when the
// city is genuinely new. Narrowed to the one method this package needs so the
// tests can stub it without standing up the geocoder.
type cityResolver interface {
	ResolveCity(ctx context.Context, name, country string) (uuid.UUID, string, error)
}

// poiUpserter promotes a confirmed submission into a real place. Narrowed to
// one method, and satisfied in production by poi.RepositoryImpl, so the
// identity rules live in one place rather than being restated here.
type poiUpserter interface {
	UpsertPOIByIdentity(ctx context.Context, name string, cityID uuid.UUID, lat, lng float64) (uuid.UUID, error)
}

type Handler struct {
	placeconnect.UnimplementedPlaceIntelligenceServiceHandler
	db     *pgxpool.Pool
	logger *slog.Logger
	cities cityResolver
	pois   poiUpserter
}

func NewHandler(db *pgxpool.Pool, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{db: db, logger: logger.With(slog.String("component", "place-intelligence"))}
}

// WithCityResolver supplies the resolver used when a submitted place names a
// city. Separate from NewHandler so the existing call sites keep working.
func (h *Handler) WithCityResolver(cities cityResolver) *Handler {
	h.cities = cities
	return h
}

// WithPOIUpserter supplies the promotion path used when a submission is
// confirmed.
func (h *Handler) WithPOIUpserter(pois poiUpserter) *Handler {
	h.pois = pois
	return h
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

// candidatePOI is a place that might be worth asking this scout about, carried
// with the columns that already answer some of the questions.
type candidatePOI struct {
	id               string
	name             string
	hasOpeningHours  bool
	hasPriceLevel    bool
	hasAccessibility bool
}

// coverage is the live-fact state of one POI: which fields are already answered
// and when the oldest of those answers was verified.
type coverage struct {
	fields   map[string]struct{}
	oldestAt time.Time
}

// ListVerificationTasks asks this scout about places they have plausibly been
// near, for the fields those places are actually missing.
//
// It used to GROUP BY over the whole points_of_interest table with no user and
// no city, so every scout saw the same arbitrary rows on every load and the
// requested fields were a hardcoded three. Both are now derived: the places
// come from the cities the user has recently browsed, and the questions are the
// contributable fields minus whatever is already known.
func (h *Handler) ListVerificationTasks(ctx context.Context, req *connect.Request[placev1.ListVerificationTasksRequest]) (*connect.Response[placev1.ListVerificationTasksResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	limit := int(req.Msg.GetLimit())
	if limit <= 0 {
		limit = 10
	}

	candidates, err := h.candidatePOIs(ctx, uid, limit)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return connect.NewResponse(&placev1.ListVerificationTasksResponse{Tasks: []*placev1.VerificationTask{}}), nil
	}

	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.id)
	}
	covered, err := h.liveCoverage(ctx, ids)
	if err != nil {
		return nil, err
	}

	tasks := make([]*placev1.VerificationTask, 0, limit)
	for _, candidate := range candidates {
		if len(tasks) >= limit {
			break
		}
		missing := missingFields(candidate, covered[candidate.id])
		if len(missing) == 0 {
			continue
		}
		task := &placev1.VerificationTask{
			PoiId:           candidate.id,
			PoiName:         candidate.name,
			RequestedFields: missing,
		}
		if oldest := covered[candidate.id].oldestAt; !oldest.IsZero() {
			task.OldestFactAt = timestamppb.New(oldest)
		}
		tasks = append(tasks, task)
	}

	return connect.NewResponse(&placev1.ListVerificationTasksResponse{Tasks: tasks}), nil
}

// missingFields is every contributable field this POI has no answer for, either
// from the crowd or from its own columns.
func missingFields(candidate candidatePOI, covered coverage) []placev1.PlaceFactField {
	missing := make([]placev1.PlaceFactField, 0, len(contributableFields))
	for _, field := range contributableFields {
		if _, known := covered.fields[fieldName(field)]; known {
			continue
		}
		switch field {
		case placev1.PlaceFactField_PLACE_FACT_FIELD_OPENING_HOURS:
			if candidate.hasOpeningHours {
				continue
			}
		case placev1.PlaceFactField_PLACE_FACT_FIELD_PRICE_LEVEL:
			if candidate.hasPriceLevel {
				continue
			}
		case placev1.PlaceFactField_PLACE_FACT_FIELD_ACCESSIBILITY:
			if candidate.hasAccessibility {
				continue
			}
		}
		missing = append(missing, field)
	}
	return missing
}

// candidatePOIs picks places to ask about: those in the cities this user has
// recently looked at, falling back to a handful of cities when they have no
// history at all.
func (h *Handler) candidatePOIs(ctx context.Context, uid uuid.UUID, limit int) ([]candidatePOI, error) {
	want := limit * candidateMultiplier
	if want > maxCandidates {
		want = maxCandidates
	}

	cityIDs, err := h.recentCityIDs(ctx, uid)
	if err != nil {
		return nil, err
	}
	if len(cityIDs) == 0 {
		if cityIDs, err = h.fallbackCityIDs(ctx); err != nil {
			return nil, err
		}
	}
	if len(cityIDs) == 0 {
		return nil, nil
	}

	rows, err := h.db.Query(ctx, `
		SELECT id::text, name,
		       opening_hours IS NOT NULL,
		       price_level IS NOT NULL,
		       COALESCE(TRIM(accessibility_info), '') <> ''
		FROM points_of_interest
		WHERE city_id = ANY($1)
		ORDER BY rating_count DESC NULLS LAST, updated_at DESC
		LIMIT $2`, cityIDs, want)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list candidate places: %w", err))
	}
	defer rows.Close()

	candidates := make([]candidatePOI, 0, want)
	for rows.Next() {
		var candidate candidatePOI
		if err := rows.Scan(&candidate.id, &candidate.name,
			&candidate.hasOpeningHours, &candidate.hasPriceLevel, &candidate.hasAccessibility); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scan candidate place: %w", err))
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("iterate candidate places: %w", err))
	}
	return candidates, nil
}

// recentCityIDs is where this user has been looking lately.
//
// poi_interactions.poi_id is a VARCHAR that is not guaranteed to hold a UUID,
// so the ids are parsed in Go rather than cast in SQL — a single malformed row
// would otherwise fail the whole query.
func (h *Handler) recentCityIDs(ctx context.Context, uid uuid.UUID) ([]uuid.UUID, error) {
	rows, err := h.db.Query(ctx, `
		SELECT DISTINCT poi_id
		FROM poi_interactions
		WHERE user_id = $1 AND timestamp > NOW() - make_interval(days => $2::int)
		LIMIT $3`, uid, recentInteractionDays, maxRecentInteractions)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list recent interactions: %w", err))
	}
	defer rows.Close()

	poiIDs := make([]uuid.UUID, 0, maxRecentInteractions)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scan recent interaction: %w", err))
		}
		if id, parseErr := uuid.Parse(raw); parseErr == nil {
			poiIDs = append(poiIDs, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("iterate recent interactions: %w", err))
	}
	if len(poiIDs) == 0 {
		return nil, nil
	}

	cityRows, err := h.db.Query(ctx, `
		SELECT DISTINCT city_id
		FROM points_of_interest
		WHERE id = ANY($1) AND city_id IS NOT NULL`, poiIDs)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("resolve recent cities: %w", err))
	}
	defer cityRows.Close()

	cityIDs := make([]uuid.UUID, 0, 8)
	for cityRows.Next() {
		var id uuid.UUID
		if err := cityRows.Scan(&id); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scan recent city: %w", err))
		}
		cityIDs = append(cityIDs, id)
	}
	if err := cityRows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("iterate recent cities: %w", err))
	}
	return cityIDs, nil
}

// fallbackCityIDs serves a scout with no history. Showing them a few real
// cities beats the old behaviour of an arbitrary slice of every POI in the
// database.
func (h *Handler) fallbackCityIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := h.db.Query(ctx, `
		SELECT id FROM cities ORDER BY updated_at DESC LIMIT $1`, fallbackCityCount)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list fallback cities: %w", err))
	}
	defer rows.Close()

	cityIDs := make([]uuid.UUID, 0, fallbackCityCount)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scan fallback city: %w", err))
		}
		cityIDs = append(cityIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("iterate fallback cities: %w", err))
	}
	return cityIDs, nil
}

// liveCoverage reports which fields already have an unexpired fact, per POI.
func (h *Handler) liveCoverage(ctx context.Context, poiIDs []string) (map[string]coverage, error) {
	// Only a corroborated fact closes a question. A single pending report must
	// keep its field on the list, or the second scout who would confirm it is
	// never asked and it can never become true.
	rows, err := h.db.Query(ctx, `
		SELECT poi_id, field, verified_at
		FROM place_facts
		WHERE poi_id = ANY($1) AND expires_at > NOW() AND contributor_count >= $2`,
		poiIDs, claimsRequiredForVerification)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("load fact coverage: %w", err))
	}
	defer rows.Close()

	covered := make(map[string]coverage, len(poiIDs))
	for rows.Next() {
		var poiID, field string
		var verifiedAt time.Time
		if err := rows.Scan(&poiID, &field, &verifiedAt); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scan fact coverage: %w", err))
		}
		entry, ok := covered[poiID]
		if !ok {
			entry = coverage{fields: make(map[string]struct{}, len(contributableFields))}
		}
		entry.fields[field] = struct{}{}
		if entry.oldestAt.IsZero() || verifiedAt.Before(entry.oldestAt) {
			entry.oldestAt = verifiedAt
		}
		covered[poiID] = entry
	}
	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("iterate fact coverage: %w", err))
	}
	return covered, nil
}

func (h *Handler) SubmitPlaceClaim(ctx context.Context, req *connect.Request[placev1.SubmitPlaceClaimRequest]) (*connect.Response[placev1.SubmitPlaceClaimResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}

	// Normalise before anything touches the database. The stored value and the
	// value corroboration compares against must be the same string, or a claim
	// can never be matched by a second scout.
	field := req.Msg.GetField()
	value, err := normalizeValue(field, req.Msg.GetValue())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	poiID := req.Msg.GetPoiId()
	poiUUID, err := uuid.Parse(poiID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("unknown place"))
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("begin place claim: %w", err))
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	// place_claims.poi_id is TEXT with no foreign key, so without this a claim
	// can be filed against a place that does not exist.
	var poiExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM points_of_interest WHERE id = $1)`, poiUUID).Scan(&poiExists); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("check place exists: %w", err))
	}
	if !poiExists {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("unknown place"))
	}

	var claimID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO place_claims (client_claim_id, poi_id, user_id, field, value, observed_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (client_claim_id) DO NOTHING
		RETURNING id`, req.Msg.GetClientClaimId(), poiID, uid,
		fieldName(field), value, req.Msg.GetObservedAt().AsTime()).Scan(&claimID)
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
		WHERE poi_id = $1 AND field = $2 AND value = $3
		  AND observed_at > NOW() - INTERVAL '180 days'`, poiID, fieldName(field), value).
		Scan(&contributorCount); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("count corroborating claims: %w", err))
	}

	status := placev1.PlaceClaimStatus_PLACE_CLAIM_STATUS_PENDING
	confidence := pendingFactConfidence
	lifetime := factLifetime(field) / 2
	if contributorCount >= claimsRequiredForVerification {
		status = placev1.PlaceClaimStatus_PLACE_CLAIM_STATUS_ACCEPTED
		confidence = 0.6 + float64(contributorCount-claimsRequiredForVerification)*0.1
		if confidence > 0.95 {
			confidence = 0.95
		}
		lifetime = factLifetime(field)
	}

	// A lone voice must not contradict a settled one. Facts are now per answer,
	// so a dissenter no longer overwrites the agreed row — they would sit beside
	// it, and a place would read as both quiet and packed. Their claim is still
	// recorded, and becomes the fact if a second scout backs it up.
	writeFact := true
	if isExclusiveField(field) && contributorCount < claimsRequiredForVerification {
		var settled bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM place_facts
				WHERE poi_id = $1 AND field = $2 AND value <> $3
				  AND expires_at > NOW() AND contributor_count >= $4
			)`, poiID, fieldName(field), value, claimsRequiredForVerification).Scan(&settled); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("check settled fact: %w", err))
		}
		writeFact = !settled
	}

	// A single claim still becomes a fact, at a low confidence and a shortened
	// life, so one scout's report is visible rather than silently discarded.
	// The WHERE on the conflict clause keeps a re-reported answer from losing
	// the support it has already gathered.
	if writeFact {
		if _, err := tx.Exec(ctx, `
		INSERT INTO place_facts (poi_id, field, value, confidence, contributor_count, verified_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), $6)
		ON CONFLICT (poi_id, field, value) DO UPDATE SET
			confidence = EXCLUDED.confidence,
			contributor_count = EXCLUDED.contributor_count,
			verified_at = NOW(),
			expires_at = EXCLUDED.expires_at,
			updated_at = NOW()
		WHERE EXCLUDED.contributor_count >= place_facts.contributor_count`,
			poiID, fieldName(field), value, confidence, contributorCount, time.Now().Add(lifetime)); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("upsert place fact: %w", err))
		}
	}

	if status == placev1.PlaceClaimStatus_PLACE_CLAIM_STATUS_ACCEPTED {
		// Facts are stored per answer so that each one corroborates on its own.
		// For a field that can only have one answer, that means the answers it
		// beat have to go, or a place would claim to be both quiet and packed.
		if isExclusiveField(field) {
			if _, err := tx.Exec(ctx, `
				DELETE FROM place_facts
				WHERE poi_id = $1 AND field = $2 AND value <> $3`,
				poiID, fieldName(field), value); err != nil {
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("clear superseded facts: %w", err))
			}
		}
		if err := creditCorroborators(ctx, tx, poiID, fieldName(field), value); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("commit place claim: %w", err))
	}
	h.logger.InfoContext(ctx, "place claim submitted",
		slog.String("claim_id", claimID.String()),
		slog.String("status", status.String()),
		slog.Int("contributors", int(contributorCount)))
	return connect.NewResponse(&placev1.SubmitPlaceClaimResponse{ClaimId: claimID.String(), Status: status}), nil
}

// creditCorroborators accepts every pending claim that agrees, and rewards each
// scout behind them.
//
// Reputation used to go only to whoever happened to submit last, even though
// their claim was worth nothing without the earlier one it matched. Everyone
// whose claim just flipped is credited.
func creditCorroborators(ctx context.Context, tx pgx.Tx, poiID, field, value string) error {
	rows, err := tx.Query(ctx, `
		UPDATE place_claims SET status = 'accepted'
		WHERE poi_id = $1 AND field = $2 AND value = $3 AND status = 'pending'
		RETURNING user_id`, poiID, field, value)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("accept corroborated claims: %w", err))
	}
	defer rows.Close()

	seen := make(map[uuid.UUID]struct{})
	credited := make([]uuid.UUID, 0, claimsRequiredForVerification)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return connect.NewError(connect.CodeInternal, fmt.Errorf("scan corroborating scout: %w", err))
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		credited = append(credited, id)
	}
	if err := rows.Err(); err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("iterate corroborating scouts: %w", err))
	}
	if len(credited) == 0 {
		return nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE contributor_profiles SET
			accepted_claims = accepted_claims + 1,
			reputation = LEAST(100, reputation + 3),
			badges = CASE WHEN accepted_claims + 1 >= 10 AND NOT ('local-scout' = ANY(badges))
				THEN array_append(badges, 'local-scout') ELSE badges END,
			updated_at = NOW()
		WHERE user_id = ANY($1)`, credited); err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("reward contributors: %w", err))
	}
	return nil
}

func (h *Handler) SubmitPlace(ctx context.Context, req *connect.Request[placev1.SubmitPlaceRequest]) (*connect.Response[placev1.SubmitPlaceResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	if h.cities == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("city resolution is not configured"))
	}

	name := strings.TrimSpace(req.Msg.GetName())
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a place needs a name"))
	}

	cityID, _, err := h.cities.ResolveCity(ctx, req.Msg.GetCityName(), req.Msg.GetCountry())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("could not place %q in a city: %w", name, err))
	}

	// Already on the guide is a different answer from "new", and the client can
	// act on it — open the place rather than propose a twin.
	if existing, found, err := h.existingPOIByIdentity(ctx, cityID, name); err != nil {
		return nil, err
	} else if found {
		return nil, connect.NewError(connect.CodeAlreadyExists,
			fmt.Errorf("%q is already on the guide (%s)", name, existing))
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("begin submission: %w", err))
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	id, err := h.insertSubmission(ctx, tx, submission{
		UserID:   uid,
		CityID:   cityID,
		Name:     name,
		Category: optionalString(req.Msg.Category),
		Address:  optionalString(req.Msg.Address),
		Website:  optionalString(req.Msg.Website),
		Lat:      req.Msg.Latitude,
		Lng:      req.Msg.Longitude,
	}, req.Msg.GetClientSubmissionId())
	if err != nil {
		return nil, err
	}

	var confirmations int32
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*)::integer FROM place_submission_confirmations WHERE submission_id = $1`, id).
		Scan(&confirmations); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("count confirmations: %w", err))
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("commit submission: %w", err))
	}
	h.logger.InfoContext(ctx, "place submitted", slog.String("submission_id", id.String()))

	return connect.NewResponse(&placev1.SubmitPlaceResponse{
		SubmissionId:        id.String(),
		Status:              placev1.PlaceSubmissionStatus_PLACE_SUBMISSION_STATUS_PENDING,
		ConfirmationsNeeded: confirmationsNeeded(confirmations),
	}), nil
}

// optionalString normalises an absent-or-blank proto field to a nil column.
func optionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
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

func (h *Handler) ConfirmPlace(ctx context.Context, req *connect.Request[placev1.ConfirmPlaceRequest]) (*connect.Response[placev1.ConfirmPlaceResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	submissionID, err := uuid.Parse(req.Msg.GetSubmissionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("unknown submission"))
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("begin confirmation: %w", err))
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var (
		submitter uuid.UUID
		cityID    uuid.UUID
		name      string
		status    string
		poiID     *uuid.UUID
		lat, lng  *float64
	)
	err = tx.QueryRow(ctx, `
		SELECT user_id, city_id, name, status, poi_id, latitude, longitude
		FROM place_submissions WHERE id = $1 FOR UPDATE`, submissionID).
		Scan(&submitter, &cityID, &name, &status, &poiID, &lat, &lng)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("unknown submission"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("load submission: %w", err))
	}

	// Already promoted: say so rather than counting another vote.
	if status == "accepted" && poiID != nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("commit confirmation: %w", err))
		}
		id := poiID.String()
		return connect.NewResponse(&placev1.ConfirmPlaceResponse{
			Status:              placev1.PlaceSubmissionStatus_PLACE_SUBMISSION_STATUS_ACCEPTED,
			ConfirmationsNeeded: 0,
			PoiId:               &id,
		}), nil
	}

	// Corroboration means somebody else. Letting a submitter confirm their own
	// place would make the whole gate decorative.
	if submitter == uid {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("a place needs somebody else to confirm it"))
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO place_submission_confirmations (submission_id, user_id)
		VALUES ($1, $2) ON CONFLICT DO NOTHING`, submissionID, uid); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("record confirmation: %w", err))
	}

	var confirmations int32
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*)::integer FROM place_submission_confirmations WHERE submission_id = $1`, submissionID).
		Scan(&confirmations); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("count confirmations: %w", err))
	}

	if needed := confirmationsNeeded(confirmations); needed > 0 {
		if err := tx.Commit(ctx); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("commit confirmation: %w", err))
		}
		return connect.NewResponse(&placev1.ConfirmPlaceResponse{
			Status:              placev1.PlaceSubmissionStatus_PLACE_SUBMISSION_STATUS_PENDING,
			ConfirmationsNeeded: needed,
		}), nil
	}

	if h.pois == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("promotion is not configured"))
	}
	var latitude, longitude float64
	if lat != nil {
		latitude = *lat
	}
	if lng != nil {
		longitude = *lng
	}
	promoted, err := h.pois.UpsertPOIByIdentity(ctx, name, cityID, latitude, longitude)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("promote submission: %w", err))
	}

	if _, err := tx.Exec(ctx, `
		UPDATE place_submissions
		SET status = 'accepted', poi_id = $2, updated_at = NOW()
		WHERE id = $1`, submissionID, promoted); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("accept submission: %w", err))
	}

	if err := creditPlaceContributors(ctx, tx, submissionID, submitter); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("commit promotion: %w", err))
	}
	h.logger.InfoContext(ctx, "place promoted",
		slog.String("submission_id", submissionID.String()), slog.String("poi_id", promoted.String()))

	id := promoted.String()
	return connect.NewResponse(&placev1.ConfirmPlaceResponse{
		Status:              placev1.PlaceSubmissionStatus_PLACE_SUBMISSION_STATUS_ACCEPTED,
		ConfirmationsNeeded: 0,
		PoiId:               &id,
	}), nil
}
